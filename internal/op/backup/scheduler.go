package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/notification"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
)

// WebDAVBackupConfig holds the WebDAV cloud backup configuration.
type WebDAVBackupConfig struct {
	Enabled       bool   `json:"enabled"`
	BaseURL       string `json:"base_url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	RemotePath    string `json:"remote_path"`
	IntervalHours int    `json:"interval_hours"`
	IncludeStats  bool   `json:"include_stats"`
	IncludeLogs   bool   `json:"include_logs"`
	MaxBackups    int    `json:"max_backups"`
}

// GetWebDAVConfig reads the current WebDAV config from settings.
func GetWebDAVConfig() (*WebDAVBackupConfig, error) {
	raw, err := setting.GetString(model.SettingKeyWebDAVConfig)
	if err != nil {
		return nil, fmt.Errorf("get webdav config: %w", err)
	}
	var cfg WebDAVBackupConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parse webdav config: %w", err)
	}
	return &cfg, nil
}

// SetWebDAVConfig persists a WebDAV config to settings.
func SetWebDAVConfig(cfg *WebDAVBackupConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal webdav config: %w", err)
	}
	return setting.SetString(model.SettingKeyWebDAVConfig, string(data))
}

// PerformWebDAVBackup exports the database and uploads it to the configured
// WebDAV server. The enabled flag is only honored for scheduled (automatic)
// backups; manual invocations skip it so that "Backup Now" works without
// requiring the auto-backup toggle to be on.
func PerformWebDAVBackup(ctx context.Context) error {
	return performWebDAVBackup(ctx, false)
}

// PerformWebDAVBackupManual runs a one-off backup triggered by the user from
// the management UI. Unlike the scheduled task it ignores the `enabled`
// toggle, since the user explicitly requested it.
func PerformWebDAVBackupManual(ctx context.Context) error {
	return performWebDAVBackup(ctx, true)
}

func performWebDAVBackup(ctx context.Context, manual bool) error {
	cfg, err := GetWebDAVConfig()
	if err != nil {
		createWebDAVBackupNotification(ctx, manual, "", 0, nil, fmt.Errorf("read config: %w", err))
		return fmt.Errorf("read config: %w", err)
	}
	if !manual && !cfg.Enabled {
		return nil
	}
	if cfg.BaseURL == "" {
		err := fmt.Errorf("webdav base URL is empty")
		createWebDAVBackupNotification(ctx, manual, "", 0, cfg, err)
		return err
	}

	client := NewWebDAVClient(cfg.BaseURL, cfg.Username, cfg.Password)

	exportFile, exportSize, err := CreateJSONExportFile(ctx, cfg.IncludeLogs, cfg.IncludeStats, true)
	if err != nil {
		createWebDAVBackupNotification(ctx, manual, "", 0, cfg, fmt.Errorf("export: %w", err))
		return fmt.Errorf("export: %w", err)
	}
	defer func() {
		_ = exportFile.Close()
		_ = os.Remove(exportFile.Name())
	}()

	filename := fmt.Sprintf("octopus-backup-%s.json", time.Now().UTC().Format("20060102-150405"))
	remotePath := strings.TrimSuffix(cfg.RemotePath, "/") + "/" + filename

	if err := client.UploadReader(remotePath, exportFile, exportSize); err != nil {
		createWebDAVBackupNotification(ctx, manual, remotePath, int(exportSize), cfg, fmt.Errorf("upload %s: %w", remotePath, err))
		return fmt.Errorf("upload %s: %w", remotePath, err)
	}

	log.Infof("webdav backup uploaded: %s (%d bytes, manual=%v)", remotePath, exportSize, manual)

	var cleanupErr error
	if cfg.MaxBackups > 0 {
		if err := cleanupOldBackups(client, cfg.RemotePath, cfg.MaxBackups); err != nil {
			cleanupErr = err
			log.Warnf("webdav backup cleanup failed: %v", err)
		}
	}

	createWebDAVBackupNotification(ctx, manual, remotePath, int(exportSize), cfg, cleanupErr)
	return nil
}

// validateBackupFilename rejects filenames containing path separators or
// traversal sequences, preventing path-traversal via user-supplied filenames
// in WebDAV restore/delete operations (2C-02).
func validateBackupFilename(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid backup filename: %q", name)
	}
	return nil
}

// RestoreFromWebDAV downloads a backup file from WebDAV and imports it.
func RestoreFromWebDAV(ctx context.Context, filename string) (*model.DBImportResult, error) {
	if err := validateBackupFilename(filename); err != nil {
		return nil, err
	}
	cfg, err := GetWebDAVConfig()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("webdav base URL is empty")
	}

	client := NewWebDAVClient(cfg.BaseURL, cfg.Username, cfg.Password)

	remotePath := strings.TrimSuffix(cfg.RemotePath, "/") + "/" + filename
	data, err := client.Download(remotePath)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", remotePath, err)
	}

	var dump model.DBDump
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, fmt.Errorf("parse backup: %w", err)
	}

	result, err := ImportWithMode(ctx, &dump, model.ImportModeIncremental)
	if err != nil {
		createWebDAVRestoreNotification(ctx, filename, nil, fmt.Errorf("import: %w", err))
		return nil, fmt.Errorf("import: %w", err)
	}
	createWebDAVRestoreNotification(ctx, filename, result, nil)

	return result, nil
}

func createWebDAVBackupNotification(ctx context.Context, manual bool, remotePath string, bytes int, cfg *WebDAVBackupConfig, err error) {
	severity := model.NotificationSeveritySuccess
	var key notification.NotifKey
	var contentArgs map[string]any
	var contentFmtArgs []any
	if err != nil {
		severity = model.NotificationSeverityError
		key = notification.KeyBackupFail
		contentArgs = map[string]any{"detail": err.Error()}
		contentFmtArgs = []any{err.Error()}
	} else if remotePath == "" {
		severity = model.NotificationSeverityWarning
		key = notification.KeyBackupSkip
		contentArgs = nil
		contentFmtArgs = nil
	} else {
		key = notification.KeyBackupOK
		contentArgs = map[string]any{"file": remotePath, "size": bytes}
		contentFmtArgs = []any{remotePath, bytes}
	}
	metadata := map[string]any{"manual": manual, "remote_path": remotePath, "bytes": bytes}
	if cfg != nil {
		metadata["include_logs"] = cfg.IncludeLogs
		metadata["include_stats"] = cfg.IncludeStats
		metadata["max_backups"] = cfg.MaxBackups
	}
	if err != nil {
		metadata["error"] = err.Error()
	}
	b, _ := json.Marshal(metadata)
	n := &model.Notification{
		Type:         model.NotificationTypeBackup,
		Severity:     severity,
		Source:       "webdav_backup",
		SourceID:     remotePath,
		DedupeKey:    fmt.Sprintf("webdav_backup:%s:%d", remotePath, time.Now().UnixMilli()),
		MetadataJSON: string(b),
		Link:         "setting",
	}
	notification.SetMessage(n, key, key, nil, contentArgs, nil, contentFmtArgs)
	if createErr := notification.Create(ctx, n); createErr != nil {
		log.Warnf("notification: failed to create webdav backup notification: %v", createErr)
	}
}

func createWebDAVRestoreNotification(ctx context.Context, filename string, result *model.DBImportResult, err error) {
	severity := model.NotificationSeveritySuccess
	var key notification.NotifKey
	var contentArgs map[string]any
	var contentFmtArgs []any
	if err != nil {
		severity = model.NotificationSeverityError
		key = notification.KeyRestoreFail
		contentArgs = map[string]any{"detail": err.Error()}
		contentFmtArgs = []any{err.Error()}
	} else {
		key = notification.KeyRestoreOK
		contentArgs = map[string]any{"file": filename}
		contentFmtArgs = []any{filename}
	}
	metadata := map[string]any{"filename": filename}
	if result != nil {
		metadata["rows_affected"] = result.RowsAffected
	}
	if err != nil {
		metadata["error"] = err.Error()
	}
	b, _ := json.Marshal(metadata)
	n := &model.Notification{
		Type:         model.NotificationTypeBackup,
		Severity:     severity,
		Source:       "webdav_restore",
		SourceID:     filename,
		DedupeKey:    fmt.Sprintf("webdav_restore:%s:%d", filename, time.Now().UnixMilli()),
		MetadataJSON: string(b),
		Link:         "setting",
	}
	notification.SetMessage(n, key, key, nil, contentArgs, nil, contentFmtArgs)
	if createErr := notification.Create(ctx, n); createErr != nil {
		log.Warnf("notification: failed to create webdav restore notification: %v", createErr)
	}
}

// ListWebDAVBackups returns available backup files from the remote WebDAV server.
// Returns an empty list (not an error) when WebDAV is not configured, so that
// frontend polling does not surface a persistent error toast.
func ListWebDAVBackups() ([]WebDAVFile, error) {
	cfg, err := GetWebDAVConfig()
	if err != nil {
		log.Warnf("webdav list: read config: %v", err)
		return nil, nil
	}
	if cfg.BaseURL == "" {
		return nil, nil
	}

	client := NewWebDAVClient(cfg.BaseURL, cfg.Username, cfg.Password)
	files, err := client.List(cfg.RemotePath)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	// Filter to only backup JSON files, exclude directories
	backups := make([]WebDAVFile, 0, len(files))
	for _, f := range files {
		if !f.IsDir && strings.HasSuffix(f.Name, ".json") {
			backups = append(backups, f)
		}
	}

	// Sort by name descending (newest first, since names contain timestamps)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Name > backups[j].Name
	})

	return backups, nil
}

// DeleteWebDAVBackup removes a specific backup file from the remote server.
func DeleteWebDAVBackup(filename string) error {
	if err := validateBackupFilename(filename); err != nil {
		return err
	}
	cfg, err := GetWebDAVConfig()
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	client := NewWebDAVClient(cfg.BaseURL, cfg.Username, cfg.Password)
	remotePath := strings.TrimSuffix(cfg.RemotePath, "/") + "/" + filename
	return client.Delete(remotePath)
}

// cleanupOldBackups removes old backup files, keeping only the newest maxBackups.
// Returns an error describing any failed deletions (joined with semicolons).
func cleanupOldBackups(client *WebDAVClient, remotePath string, maxBackups int) error {
	files, err := client.List(remotePath)
	if err != nil {
		return fmt.Errorf("list for cleanup: %w", err)
	}

	var backups []WebDAVFile
	for _, f := range files {
		if !f.IsDir && strings.HasSuffix(f.Name, ".json") {
			backups = append(backups, f)
		}
	}

	if len(backups) <= maxBackups {
		return nil
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Name > backups[j].Name
	})

	var deleteErrors []string
	for _, f := range backups[maxBackups:] {
		if err := client.Delete(f.Path); err != nil {
			errMsg := fmt.Sprintf("%s: %v", f.Name, err)
			log.Warnf("failed to delete old backup %s: %v", f.Name, err)
			deleteErrors = append(deleteErrors, errMsg)
		} else {
			log.Infof("deleted old webdav backup: %s", f.Name)
		}
	}

	if len(deleteErrors) > 0 {
		return fmt.Errorf("failed to delete %d old backups: %s", len(deleteErrors), strings.Join(deleteErrors, "; "))
	}
	return nil
}
