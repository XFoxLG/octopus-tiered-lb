package handlers

import (
	"bytes"
	"errors"
	"fmt"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/backup"
	stg "github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
	"github.com/lingyuins/octopus/internal/task"
	"github.com/lingyuins/octopus/internal/utils/log"
)

var (
	maxDBImportBytes               int64 = 256 << 20 // 256 MiB：支持大备份导入（issue #158）
	maxDBImportMultipartExtraBytes int64 = 1 << 20
)

func init() {
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermSettingsRead)).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getSettingList),
		).
		AddRoute(
			router.NewRoute("/set", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(setSetting),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/import", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Handle(importDB),
		).
		AddRoute(
			router.NewRoute("/cache/config", http.MethodGet).
				Handle(getCacheConfig),
		).
		AddRoute(
			router.NewRoute("/cache/preview", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(previewCacheConnection),
		).
		AddRoute(
			router.NewRoute("/cache/test", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(testCacheConnection),
		).
		AddRoute(
			router.NewRoute("/cache/save", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(saveCacheConfig),
		)
}

func getSettingList(c *gin.Context) {
	settings, err := stg.List(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	if isViewerRole(c.GetString("user_role")) {
		redactSettingsURLsForViewer(settings)
	}
	resp.Success(c, settings)
}

func setSetting(c *gin.Context) {
	var setting model.Setting
	if err := c.ShouldBindJSON(&setting); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := setting.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := stg.SetString(setting.Key, setting.Value); err != nil {
		resp.InternalError(c)
		return
	}
	// Setting is now persisted. All downstream effects are best-effort:
	// log failures but do not return an error status to the client,
	// which would misleadingly suggest the setting was NOT saved.
	if shouldInvalidateModelMarket(setting.Key) {
		op.ModelMarketInvalidateCache()
	}
	switch setting.Key {
	case model.SettingKeyStatsSaveInterval:
		minutes, err := strconv.Atoi(setting.Value)
		if err != nil {
			log.Warnf("invalid stats_save_interval value %q after persist: %v", setting.Value, err)
			break
		}
		interval := time.Duration(minutes) * time.Minute
		task.Update(task.TaskStatsSave, interval)
		task.Update(task.TaskRuntimeState, interval)
	case model.SettingKeyModelInfoUpdateInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			log.Warnf("invalid model_info_update_interval value %q after persist: %v", setting.Value, err)
			break
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	case model.SettingKeySyncLLMInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			log.Warnf("invalid sync_llm_interval value %q after persist: %v", setting.Value, err)
			break
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	case model.SettingKeyLogLevel:
		log.SetLevel(setting.Value)
	case model.SettingKeyRelayLogKeepEnabled:
		// 独立日志库模式下：关闭日志则断开日志库连接，开启则重连。
		// 共用主库时为空操作。失败仅记录，不影响设置已持久化的事实。
		enabled, err := strconv.ParseBool(setting.Value)
		if err != nil {
			log.Warnf("invalid relay_log_keep_enabled value %q after persist: %v", setting.Value, err)
			break
		}
		if err := op.RelayLogApplyKeepEnabled(c.Request.Context(), enabled); err != nil {
			log.Warnf("failed to apply log database lifecycle after toggling relay_log_keep_enabled: %v", err)
		}
	}
	resp.Success(c, setting)
}

func shouldInvalidateModelMarket(key model.SettingKey) bool {
	switch key {
	case model.SettingKeyModelNormalizeRouterPrefixes,
		model.SettingKeyModelNormalizeFunctionalSuffixes,
		model.SettingKeyModelNormalizeExplicitMappings,
		model.SettingKeyModelNormalizeMarketDedupeDefault:
		return true
	default:
		return false
	}
}

// exportDB 导出全库数据为 JSON 文件下载。
//
// 这是一个下载型接口（Content-Disposition: attachment），直接返回原始 JSON dump
// 供浏览器保存为文件，不使用管理端标准 {code, message, data} envelope——
// 这是有意例外，不是遗漏。
func exportDB(c *gin.Context) {
	includeLogs, _ := strconv.ParseBool(c.DefaultQuery("include_logs", "false"))
	includeStats, _ := strconv.ParseBool(c.DefaultQuery("include_stats", "false"))

	exportFile, exportSize, err := backup.CreateJSONExportFile(c.Request.Context(), includeLogs, includeStats, true)
	if err != nil {
		resp.InternalError(c)
		return
	}
	defer func() {
		_ = exportFile.Close()
		_ = os.Remove(exportFile.Name())
	}()

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=\"octopus-export-"+time.Now().Format("20060102150405")+".json\"")
	c.Header("Content-Length", strconv.FormatInt(exportSize, 10))
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, exportFile); err != nil {
		log.Warnf("stream database export failed: %v", err)
	}
}

func importDB(c *gin.Context) {
	var dump model.DBDump
	defer cleanupDBImportMultipartForm(c)

	if err := readDBDump(c, &dump); err != nil {
		status := http.StatusBadRequest
		if isDBImportTooLarge(err) {
			status = http.StatusRequestEntityTooLarge
		}
		resp.Error(c, status, err.Error())
		return
	}

	mode := c.DefaultQuery("mode", model.ImportModeIncremental)
	if mode != model.ImportModeIncremental && mode != model.ImportModeFull {
		resp.Error(c, http.StatusBadRequest, fmt.Sprintf("invalid import mode: %s (use 'incremental' or 'full')", mode))
		return
	}

	result, err := backup.ImportWithMode(c.Request.Context(), &dump, mode)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := op.InitCache(); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, result)
}

func decodeDBDump(body []byte, dump *model.DBDump) error {
	return decodeDBDumpReader(bytes.NewReader(body), dump)
}

func readDBDump(c *gin.Context, dump *model.DBDump) error {
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		limitDBImportRequestBody(c)
		fh, err := c.FormFile("file")
		if err != nil {
			return normalizeDBImportMultipartError(err)
		}
		if fh.Size > 0 && fh.Size > maxDBImportBytes {
			return newDBImportTooLargeError()
		}

		f, err := fh.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		return decodeDBDumpReader(f, dump)
	}

	return decodeDBDumpReader(c.Request.Body, dump)
}

func cleanupDBImportMultipartForm(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.MultipartForm == nil {
		return
	}
	_ = c.Request.MultipartForm.RemoveAll()
}

func limitDBImportRequestBody(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDBImportBytes+maxDBImportMultipartExtraBytes)
}

func normalizeDBImportMultipartError(err error) error {
	if err == nil {
		return nil
	}
	if isHTTPMaxBytesError(err) {
		return newDBImportTooLargeError()
	}
	if errors.Is(err, http.ErrMissingFile) {
		return fmt.Errorf("missing upload file field 'file'")
	}
	return err
}

func decodeDBDumpReader(r io.Reader, dump *model.DBDump) error {
	limitedReader := &io.LimitedReader{R: r, N: maxDBImportBytes + 1}
	if dump == nil {
		var empty struct{}
		if err := utilsjson.NewDecoder(limitedReader).Decode(&empty); err != nil {
			if limitedReader.N <= 0 {
				return newDBImportTooLargeError()
			}
			return err
		}
		if limitedReader.N <= 0 {
			return newDBImportTooLargeError()
		}
		return nil
	}

	var envelope struct {
		model.DBDump
		Code    int                  `json:"code"`
		Message string               `json:"message"`
		Data    utilsjson.RawMessage `json:"data"`
	}
	if err := utilsjson.NewDecoder(limitedReader).Decode(&envelope); err != nil {
		if limitedReader.N <= 0 {
			return newDBImportTooLargeError()
		}
		return err
	}
	if limitedReader.N <= 0 {
		return newDBImportTooLargeError()
	}

	*dump = envelope.DBDump

	if isEmptyDBDump(*dump) && len(envelope.Data) > 0 {
		if err := utilsjson.Unmarshal(envelope.Data, dump); err != nil {
			return err
		}
	}

	return nil
}

func isEmptyDBDump(dump model.DBDump) bool {
	return dump.Version == 0 &&
		len(dump.Channels) == 0 &&
		len(dump.ChannelKeys) == 0 &&
		len(dump.Groups) == 0 &&
		len(dump.GroupItems) == 0 &&
		len(dump.Settings) == 0 &&
		len(dump.APIKeys) == 0 &&
		len(dump.LLMInfos) == 0 &&
		len(dump.RelayLogs) == 0 &&
		len(dump.StatsDaily) == 0 &&
		len(dump.StatsHourly) == 0 &&
		len(dump.StatsTotal) == 0 &&
		len(dump.StatsChannel) == 0 &&
		len(dump.StatsModel) == 0 &&
		len(dump.StatsAPIKey) == 0
}

func isDBImportTooLarge(err error) bool {
	return err != nil && strings.Contains(err.Error(), "backup file exceeds")
}

func isHTTPMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func newDBImportTooLargeError() error {
	return fmt.Errorf("backup file exceeds %s import limit; retry without logs/stats or use a database-level backup for larger datasets", formatDBImportLimit(maxDBImportBytes))
}

func formatDBImportLimit(limit int64) string {
	switch {
	case limit%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", limit>>20)
	case limit%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", limit>>10)
	default:
		return fmt.Sprintf("%d bytes", limit)
	}
}
