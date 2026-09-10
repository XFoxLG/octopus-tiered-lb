package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const dbDumpVersion = 2
const maxRelayLogsExport = 500_000
const maxAuditLogsExport = 500_000
const batchInsertSize = 1000 // 分批插入：每批最多 1000 行（避免 SQLite 参数限制）

func ExportAll(ctx context.Context, includeLogs, includeStats bool) (*model.DBDump, error) {
	conn := db.GetDB().WithContext(ctx)

	d := &model.DBDump{
		Version:      dbDumpVersion,
		ExportedAt:   time.Now().UTC(),
		IncludeLogs:  includeLogs,
		IncludeStats: includeStats,
	}

	// Core tables
	if err := conn.Find(&d.Channels).Error; err != nil {
		return nil, fmt.Errorf("export channels: %w", err)
	}
	if err := conn.Find(&d.ChannelKeys).Error; err != nil {
		return nil, fmt.Errorf("export channel_keys: %w", err)
	}
	if err := conn.Find(&d.ChannelGroups).Error; err != nil {
		return nil, fmt.Errorf("export channel_groups: %w", err)
	}
	if err := conn.Find(&d.Groups).Error; err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}
	if err := conn.Find(&d.GroupItems).Error; err != nil {
		return nil, fmt.Errorf("export group_items: %w", err)
	}
	if err := conn.Find(&d.LLMInfos).Error; err != nil {
		return nil, fmt.Errorf("export llm_infos: %w", err)
	}
	if err := conn.Find(&d.APIKeys).Error; err != nil {
		return nil, fmt.Errorf("export api_keys: %w", err)
	}
	if err := conn.Find(&d.Users).Error; err != nil {
		return nil, fmt.Errorf("export users: %w", err)
	}
	if err := conn.Find(&d.Settings).Error; err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}

	// Notifications
	if err := conn.Find(&d.Notifications).Error; err != nil {
		return nil, fmt.Errorf("export notifications: %w", err)
	}

	// Runtime
	if err := conn.Find(&d.RuntimeStates).Error; err != nil {
		return nil, fmt.Errorf("export runtime_states: %w", err)
	}
	if err := conn.Find(&d.CircuitBreakerStates).Error; err != nil {
		return nil, fmt.Errorf("export circuit_breaker_states: %w", err)
	}

	if includeStats {
		if err := conn.Find(&d.StatsTotal).Error; err != nil {
			return nil, fmt.Errorf("export stats_total: %w", err)
		}
		if err := conn.Find(&d.StatsDaily).Error; err != nil {
			return nil, fmt.Errorf("export stats_daily: %w", err)
		}
		if err := conn.Find(&d.StatsHourly).Error; err != nil {
			return nil, fmt.Errorf("export stats_hourly: %w", err)
		}
		if err := conn.Find(&d.StatsModel).Error; err != nil {
			return nil, fmt.Errorf("export stats_model: %w", err)
		}
		if err := conn.Find(&d.StatsChannel).Error; err != nil {
			return nil, fmt.Errorf("export stats_channel: %w", err)
		}
		if err := conn.Find(&d.StatsAPIKey).Error; err != nil {
			return nil, fmt.Errorf("export stats_api_key: %w", err)
		}
	}

	if includeLogs {
		if err := conn.Order("id DESC").Limit(maxAuditLogsExport).Find(&d.AuditLogs).Error; err != nil {
			return nil, fmt.Errorf("export audit_logs: %w", err)
		}
		// relay_logs 可能位于独立日志库，从日志库连接读取（共用主库时 GetLogDB
		// 返回主库连接，行为不变）。强制导出：无论「保留历史日志」开关是否开启，
		// 都导出日志库的实际内容；若独立日志库此前被 CloseLogDB 断开，先重连再读。
		if db.IsLogDBSeparate() {
			if err := db.ReopenLogDB(); err != nil {
				return nil, fmt.Errorf("reopen log db before export: %w", err)
			}
		}
		if logConn := db.GetLogDB(); logConn != nil {
			if err := logConn.WithContext(ctx).Order("id DESC").Limit(maxRelayLogsExport).Find(&d.RelayLogs).Error; err != nil {
				return nil, fmt.Errorf("export relay_logs: %w", err)
			}
			if err := exportRelayLogFamily(ctx, logConn, d); err != nil {
				return nil, err
			}
		}
	}

	// API credential profiles (Tools: CLI credential verification/export)
	if err := conn.Find(&d.APICredentialProfiles).Error; err != nil {
		return nil, fmt.Errorf("export api_credential_profiles: %w", err)
	}

	return d, nil
}

func exportRelayLogFamily(ctx context.Context, logConnection *gorm.DB, dump *model.DBDump) error {
	if logConnection == nil || dump == nil || len(dump.RelayLogs) == 0 {
		return nil
	}
	digestSet := make(map[string]struct{})
	for offset := 0; offset < len(dump.RelayLogs); offset += batchInsertSize {
		end := offset + batchInsertSize
		if end > len(dump.RelayLogs) {
			end = len(dump.RelayLogs)
		}
		relayLogIDs := make([]int64, 0, end-offset)
		for relayLogIndex := offset; relayLogIndex < end; relayLogIndex++ {
			relayLogIDs = append(relayLogIDs, dump.RelayLogs[relayLogIndex].ID)
		}
		var attempts []model.RelayLogAttempt
		if err := logConnection.WithContext(ctx).
			Where("relay_log_id IN ?", relayLogIDs).
			Order("relay_log_id ASC, attempt_num ASC, id ASC").
			Find(&attempts).Error; err != nil {
			return fmt.Errorf("export relay_log_attempts: %w", err)
		}
		dump.RelayLogAttempts = append(dump.RelayLogAttempts, attempts...)

		var contentRefs []model.RelayLogContentRef
		if err := logConnection.WithContext(ctx).
			Where("relay_log_id IN ?", relayLogIDs).
			Order("relay_log_id ASC, attempt_num ASC, boundary ASC, slot ASC, id ASC").
			Find(&contentRefs).Error; err != nil {
			return fmt.Errorf("export relay_log_content_refs: %w", err)
		}
		for _, contentRef := range contentRefs {
			if contentRef.BlobDigest != "" {
				digestSet[contentRef.BlobDigest] = struct{}{}
			}
		}
		dump.RelayLogContentRefs = append(dump.RelayLogContentRefs, contentRefs...)
	}

	digests := make([]string, 0, len(digestSet))
	for digest := range digestSet {
		digests = append(digests, digest)
	}
	for offset := 0; offset < len(digests); offset += batchInsertSize {
		end := offset + batchInsertSize
		if end > len(digests) {
			end = len(digests)
		}
		var blobs []model.RelayLogContentBlob
		if err := logConnection.WithContext(ctx).
			Where("digest IN ?", digests[offset:end]).
			Find(&blobs).Error; err != nil {
			return fmt.Errorf("export relay_log_content_blobs: %w", err)
		}
		for _, blob := range blobs {
			dump.RelayLogContentBlobs = append(dump.RelayLogContentBlobs, model.RelayLogContentBlobDump{
				Digest:       blob.Digest,
				Encoding:     blob.Encoding,
				OriginalSize: blob.OriginalSize,
				StoredSize:   blob.StoredSize,
				Payload:      append([]byte(nil), blob.Payload...),
				CreatedAt:    blob.CreatedAt,
			})
		}
	}
	return nil
}

func decodeRelayLogBackupBlob(blob model.RelayLogContentBlobDump) ([]byte, error) {
	if blob.StoredSize != int64(len(blob.Payload)) {
		return nil, fmt.Errorf("blob %s stored size is %d, payload has %d bytes", blob.Digest, blob.StoredSize, len(blob.Payload))
	}
	var decoded []byte
	switch blob.Encoding {
	case "", "identity":
		decoded = append([]byte(nil), blob.Payload...)
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(blob.Payload))
		if err != nil {
			return nil, fmt.Errorf("blob %s gzip header: %w", blob.Digest, err)
		}
		decoded, err = io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, fmt.Errorf("blob %s gzip payload: %w", blob.Digest, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("blob %s gzip close: %w", blob.Digest, closeErr)
		}
	default:
		return nil, fmt.Errorf("blob %s has unsupported encoding %q", blob.Digest, blob.Encoding)
	}
	if blob.OriginalSize != int64(len(decoded)) {
		return nil, fmt.Errorf("blob %s original size is %d, decoded payload has %d bytes", blob.Digest, blob.OriginalSize, len(decoded))
	}
	digestBytes := sha256.Sum256(decoded)
	if actualDigest := hex.EncodeToString(digestBytes[:]); actualDigest != blob.Digest {
		return nil, fmt.Errorf("blob digest mismatch: expected %s, got %s", blob.Digest, actualDigest)
	}
	return decoded, nil
}

func relayLogBackupBlobs(dump *model.DBDump) ([]model.RelayLogContentBlob, map[string]struct{}, error) {
	if dump == nil || len(dump.RelayLogContentBlobs) == 0 {
		return nil, nil, nil
	}
	blobs := make([]model.RelayLogContentBlob, 0, len(dump.RelayLogContentBlobs))
	digests := make(map[string]struct{}, len(dump.RelayLogContentBlobs))
	for _, dumpBlob := range dump.RelayLogContentBlobs {
		if strings.TrimSpace(dumpBlob.Digest) == "" {
			return nil, nil, fmt.Errorf("relay log content blob has an empty digest")
		}
		if _, duplicate := digests[dumpBlob.Digest]; duplicate {
			return nil, nil, fmt.Errorf("relay log content blob %s is duplicated", dumpBlob.Digest)
		}
		if _, err := decodeRelayLogBackupBlob(dumpBlob); err != nil {
			return nil, nil, err
		}
		digests[dumpBlob.Digest] = struct{}{}
		blobs = append(blobs, model.RelayLogContentBlob{
			Digest:       dumpBlob.Digest,
			Encoding:     dumpBlob.Encoding,
			OriginalSize: dumpBlob.OriginalSize,
			StoredSize:   dumpBlob.StoredSize,
			Payload:      append([]byte(nil), dumpBlob.Payload...),
			CreatedAt:    dumpBlob.CreatedAt,
		})
	}
	return blobs, digests, nil
}

func deriveRelayLogAttemptRows(relayLogs []model.RelayLog) []model.RelayLogAttempt {
	rows := make([]model.RelayLogAttempt, 0)
	for _, relayLog := range relayLogs {
		for _, attempt := range relayLog.Attempts {
			if attempt.ChannelID == 0 {
				continue
			}
			rows = append(rows, model.RelayLogAttempt{
				RelayLogID:       relayLog.ID,
				AttemptNum:       attempt.AttemptNum,
				ChannelID:        attempt.ChannelID,
				ChannelKeyID:     attempt.ChannelKeyID,
				ChannelName:      attempt.ChannelName,
				ModelName:        attempt.ModelName,
				AdapterType:      attempt.AdapterType,
				Status:           string(attempt.Status),
				HTTPStatus:       attempt.HTTPStatus,
				RequestPrepared:  attempt.RequestPrepared,
				SendStarted:      attempt.SendStarted,
				RequestBytes:     attempt.RequestBytes,
				RequestComplete:  attempt.RequestComplete,
				ResponseReceived: attempt.ResponseReceived,
				ResponseBytes:    attempt.ResponseBytes,
				ResponseComplete: attempt.ResponseComplete,
				Duration:         attempt.Duration,
				Sticky:           attempt.Sticky,
				Msg:              attempt.Msg,
				Time:             relayLog.Time,
			})
		}
	}
	return rows
}

func importRelayLogFamily(cfg *importConfig, dump *model.DBDump) error {
	if cfg == nil || cfg.conn == nil || dump == nil || (!dump.IncludeLogs && !cfg.isFull) {
		return nil
	}

	blobs, blobDigests, err := relayLogBackupBlobs(dump)
	if err != nil {
		return fmt.Errorf("validate relay log content blobs: %w", err)
	}

	acceptedRelayLogs := append([]model.RelayLog(nil), dump.RelayLogs...)
	acceptedLogIDs := make(map[int64]struct{}, len(acceptedRelayLogs))
	if !cfg.isFull && len(acceptedRelayLogs) > 0 {
		candidateIDs := make([]int64, 0, len(acceptedRelayLogs))
		for _, relayLog := range acceptedRelayLogs {
			candidateIDs = append(candidateIDs, relayLog.ID)
		}
		var existingIDs []int64
		if err := cfg.conn.Model(&model.RelayLog{}).Where("id IN ?", candidateIDs).Pluck("id", &existingIDs).Error; err != nil {
			return fmt.Errorf("query existing relay logs: %w", err)
		}
		existingSet := make(map[int64]struct{}, len(existingIDs))
		for _, relayLogID := range existingIDs {
			existingSet[relayLogID] = struct{}{}
		}
		filtered := acceptedRelayLogs[:0]
		for _, relayLog := range acceptedRelayLogs {
			if _, exists := existingSet[relayLog.ID]; !exists {
				filtered = append(filtered, relayLog)
			}
		}
		acceptedRelayLogs = filtered
	}
	for _, relayLog := range acceptedRelayLogs {
		if relayLog.ID == 0 {
			return fmt.Errorf("relay log backup contains zero parent ID")
		}
		acceptedLogIDs[relayLog.ID] = struct{}{}
	}

	contentRefs := make([]model.RelayLogContentRef, 0, len(dump.RelayLogContentRefs))
	requiredBlobDigests := make(map[string]struct{})
	for _, contentRef := range dump.RelayLogContentRefs {
		if _, accepted := acceptedLogIDs[contentRef.RelayLogID]; !accepted {
			continue
		}
		if contentRef.State == model.RelayLogContentStateReady {
			if contentRef.BlobDigest == "" {
				return fmt.Errorf("ready relay log content ref for log %d has no blob digest", contentRef.RelayLogID)
			}
			if _, exists := blobDigests[contentRef.BlobDigest]; !exists {
				return fmt.Errorf("relay log content ref for log %d points to missing blob %s", contentRef.RelayLogID, contentRef.BlobDigest)
			}
			requiredBlobDigests[contentRef.BlobDigest] = struct{}{}
		}
		contentRef.ID = 0
		contentRefs = append(contentRefs, contentRef)
	}
	filteredBlobs := make([]model.RelayLogContentBlob, 0, len(requiredBlobDigests))
	for _, blob := range blobs {
		if _, required := requiredBlobDigests[blob.Digest]; required {
			filteredBlobs = append(filteredBlobs, blob)
		}
	}
	blobs = filteredBlobs

	attemptRows := dump.RelayLogAttempts
	if len(attemptRows) == 0 {
		attemptRows = deriveRelayLogAttemptRows(acceptedRelayLogs)
	}
	filteredAttempts := make([]model.RelayLogAttempt, 0, len(attemptRows))
	for _, attempt := range attemptRows {
		if _, accepted := acceptedLogIDs[attempt.RelayLogID]; !accepted {
			continue
		}
		attempt.ID = 0
		filteredAttempts = append(filteredAttempts, attempt)
	}

	if cfg.isFull {
		for _, table := range []string{"relay_log_content_refs", "relay_log_attempts", "relay_log_content_blobs", "relay_logs"} {
			if err := cfg.deleteAll(table); err != nil {
				return fmt.Errorf("full import: delete %s: %w", table, err)
			}
		}
	}
	if err := cfg.doNothing("relay_logs", &acceptedRelayLogs, len(acceptedRelayLogs)); err != nil {
		return err
	}
	if err := cfg.doNothing("relay_log_content_blobs", &blobs, len(blobs)); err != nil {
		return err
	}
	if err := cfg.doNothing("relay_log_attempts", &filteredAttempts, len(filteredAttempts)); err != nil {
		return err
	}
	if err := cfg.doNothing("relay_log_content_refs", &contentRefs, len(contentRefs)); err != nil {
		return err
	}
	return nil
}

type importConfig struct {
	conn    *gorm.DB
	res     *model.DBImportResult
	version int // dump version, 0 for old backward-compat
	isFull  bool
}

func appendStep(res *model.DBImportResult, table, mode string, rows int64, err error) {
	step := model.DBImportStep{Table: table, Mode: mode, RowsAffected: rows, OK: err == nil}
	if err != nil {
		step.Error = err.Error()
	}
	res.Progress = append(res.Progress, step)
}

func (c *importConfig) doNothing(table string, rows any, count int) error {
	if count == 0 {
		return nil
	}
	return c.batchInsert(table, rows, count, clause.OnConflict{DoNothing: true}, "insert")
}

func (c *importConfig) upsertAll(table string, rows any, count int, conflictColumns []clause.Column) error {
	if count == 0 {
		return nil
	}
	return c.batchInsert(table, rows, count, clause.OnConflict{
		Columns:   conflictColumns,
		UpdateAll: true,
	}, "upsert")
}

// batchInsert 将大批量数据分批插入，避免单次事务过大导致超时或内存溢出。
// rows 必须是指向切片的指针（如 *[]model.Channel）。
//
// 注意：Channel.Enabled / ChannelKey.Enabled 去掉了 gorm:"default:true"
// tag（issue #199），因此 GORM Create 会把 enabled=false 原样写入 INSERT，
// 而不再因零值替换 + DB 默认值把禁用渠道恢复成启用状态。
func (c *importConfig) batchInsert(table string, rows any, count int, conflict clause.OnConflict, mode string) error {
	if count == 0 {
		return nil
	}

	// 反射获取切片
	rv := reflect.ValueOf(rows)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("%s: rows must be *[]T, got %T", table, rows)
	}
	slice := rv.Elem()
	totalRows := slice.Len()
	if totalRows == 0 {
		return nil
	}

	var totalAffected int64
	for offset := 0; offset < totalRows; offset += batchInsertSize {
		end := offset + batchInsertSize
		if end > totalRows {
			end = totalRows
		}
		batch := slice.Slice(offset, end).Interface()

		result := c.conn.Table(table).Clauses(conflict).Create(batch)
		if result.Error != nil {
			appendStep(c.res, table, mode, totalAffected, result.Error)
			return fmt.Errorf("%s: batch [%d:%d]: %w", table, offset, end, result.Error)
		}
		totalAffected += result.RowsAffected
	}

	appendStep(c.res, table, mode, totalAffected, nil)
	return nil
}

func (c *importConfig) upsertSettings(rows []model.Setting) error {
	if len(rows) == 0 {
		return nil
	}
	result := c.conn.Table("settings").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&rows)
	appendStep(c.res, "settings", "upsert", result.RowsAffected, result.Error)
	if result.Error != nil {
		return fmt.Errorf("settings: %w", result.Error)
	}
	return nil
}

func (c *importConfig) deleteAll(table string) error {
	// 用方言感知的引号转义表名（MySQL 反引号、Postgres 双引号、SQLite 反引号），
	// 避免 groups 等 MySQL 保留字导致 Error 1064 语法错误。
	quoted := quoteTableName(c.conn, table)
	result := c.conn.Exec(fmt.Sprintf("DELETE FROM %s", quoted))
	appendStep(c.res, table, "delete", result.RowsAffected, result.Error)
	return result.Error
}

// quoteTableName 用 GORM Dialector 做方言感知的表名转义。
func quoteTableName(conn *gorm.DB, table string) string {
	var b strings.Builder
	conn.Dialector.QuoteTo(&b, table)
	return b.String()
}

// disableForeignKeyChecks 在目标会话内临时关闭外键校验，返回恢复函数。
// 覆盖跨库迁移导入时源库残留的孤立子表行（issue #112）。
//
// 方言处理：
//   - MySQL：SET FOREIGN_KEY_CHECKS=0 / =1（会话级，不影响连接池其他会话）
//   - Postgres：SET session_replication_role='replica' / ='origin'（禁用触发器，
//     含外键；会话级）
//   - SQLite：PRAGMA foreign_keys=OFF / =ON（per-connection；SQLite 连接池
//     MaxOpenConns=1，故等同于会话级。必须在事务外设置，这里在 Transaction
//     之前调用，满足该约束）
//
// 返回的 restore 函数保证外键校验在导入后被恢复，即便导入中途出错。
// 调用方应在 defer 中调用 restore。当 disable 失败时 restore 为 nil，
// 导入按原行为继续，由数据库自身约束兜底。
func disableForeignKeyChecks(conn *gorm.DB) (func() error, error) {
	if conn == nil || conn.Dialector == nil {
		return nil, nil
	}
	switch conn.Dialector.Name() {
	case "mysql":
		if err := conn.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return nil, err
		}
		return func() error { return conn.Exec("SET FOREIGN_KEY_CHECKS = 1").Error }, nil
	case "postgres":
		if err := conn.Exec("SET session_replication_role = 'replica'").Error; err != nil {
			return nil, err
		}
		return func() error { return conn.Exec("SET session_replication_role = 'origin'").Error }, nil
	case "sqlite":
		// PRAGMA foreign_keys 不能在事务内更改；本函数在 Transaction 调用之前
		// 执行，满足该约束。SQLite 单连接池下 per-connection 即会话级。
		if err := conn.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
			return nil, err
		}
		return func() error { return conn.Exec("PRAGMA foreign_keys = ON").Error }, nil
	default:
		// 未知方言：无法安全关闭外键，跳过，由数据库自身约束兜底。
		return nil, nil
	}
}

func ImportWithMode(ctx context.Context, dump *model.DBDump, mode string) (*model.DBImportResult, error) {
	return ImportWithModeToDB(ctx, db.GetDB(), dump, mode)
}

func ImportWithModeToDB(ctx context.Context, target *gorm.DB, dump *model.DBDump, mode string) (*model.DBImportResult, error) {
	if dump == nil {
		return nil, fmt.Errorf("empty dump")
	}
	if target == nil {
		return nil, fmt.Errorf("target database is nil")
	}
	isFull := mode == model.ImportModeFull
	res := &model.DBImportResult{RowsAffected: map[string]int64{}}
	cfg := &importConfig{conn: target.WithContext(ctx), res: res, isFull: isFull, version: dump.Version}

	// relay_logs 是否需要路由到独立日志库：仅在「live 导入」（target 即主库）
	// 且配置了独立日志库时成立。此时 relay_logs 落在另一个数据库，无法纳入主库
	// 事务，需在事务外单独处理（日志可丢，跨库非原子可接受）。
	// 迁移路径（target 为另开的库）不走这里，relay_logs 跟随 target 一起迁移，
	// 行为与旧版一致。
	logToSeparateDB := target == db.GetDB() && db.IsLogDBSeparate()
	maintenanceSucceeded := false
	if target == db.GetDB() && (dump.IncludeLogs || isFull) {
		finishMaintenance := relaylog.BeginMaintenance(isFull)
		defer func() { finishMaintenance(maintenanceSucceeded) }()
	}

	// 跨库迁移导入时，源库（尤其 SQLite，历史上 foreign_keys 默认 OFF）可能
	// 残留孤立的子表行：父行已删但子行（stats_channel / channel_keys / site_*
	// 等）未同步清理。GORM 依据 `foreignKey:` struct tag 在 MySQL/Postgres/SQLite
	// 目标库建出真实外键约束，导入这些孤立行会触发 Error 1452 / 23503 / 787
	// （issue #112）。这里在导入期间临时关闭目标会话的外键校验，导入完成后恢复
	// ——会话级变量（SQLite 为 per-connection，单连接池等同会话级），不影响连接池
	// 里其他会话。
	fkRestore, fkErr := disableForeignKeyChecks(cfg.conn)
	defer func() {
		if fkRestore != nil {
			_ = fkRestore()
		}
	}()
	if fkErr != nil {
		// 仅记录，不阻断导入：关闭失败时按原行为继续，由数据库自身约束兜底。
		fkRestore = nil
	}

	err := cfg.conn.Transaction(func(tx *gorm.DB) error {
		cfg.conn = tx

		if isFull {
			// Delete in reverse dependency order to avoid FK violations.
			// users is deliberately excluded: User.Password is json:"-",
			// so backups never carry password hashes — deleting users would
			// leave an empty table with no way to log in (issue: full restore
			// locked out admin). The users table is auth infrastructure, not
			// application data, and must survive a restore.
			deleteOrder := []string{
				"stats_api_keys", "stats_channels", "stats_models",
				"stats_hourlies", "stats_dailies", "stats_totals",
				"group_items", "channel_groups", "groups",
				"notifications",
				"audit_logs", "auto_strategy_states", "circuit_breaker_states",
				"api_keys", "channel_keys", "channels",
				"llm_infos", "settings",
			}
			for i, table := range deleteOrder {
				switch table {
				case "stats_totals":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_total")
				case "stats_dailies":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_daily")
				case "stats_hourlies":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_hourly")
				case "stats_models":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_model")
				case "stats_channels":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_channel")
				case "stats_api_keys":
					deleteOrder[i] = cfg.conn.NamingStrategy.TableName("stats_api_key")
				case "auto_strategy_states":
					deleteOrder[i] = "auto_strategy_states"
				}
			}
			for _, table := range deleteOrder {
				// relay_logs 路由到独立日志库时，由事务外的 importRelayLogsToLogDB
				// 负责清空与写入，主事务（主库）跳过它。
				if table == "relay_logs" && logToSeparateDB {
					continue
				}
				if err := cfg.deleteAll(table); err != nil {
					return fmt.Errorf("full import: delete %s: %w", table, err)
				}
			}
		}

		// Import channels / keys / groups / items — skip existing
		if err := cfg.doNothing("channels", &dump.Channels, len(dump.Channels)); err != nil {
			return err
		}
		if err := cfg.doNothing("channel_keys", &dump.ChannelKeys, len(dump.ChannelKeys)); err != nil {
			return err
		}
		if err := cfg.doNothing("channel_groups", &dump.ChannelGroups, len(dump.ChannelGroups)); err != nil {
			return err
		}
		if err := cfg.doNothing("groups", &dump.Groups, len(dump.Groups)); err != nil {
			return err
		}
		if err := cfg.doNothing("group_items", &dump.GroupItems, len(dump.GroupItems)); err != nil {
			return err
		}

		// LLM prices — upsert by name
		if err := cfg.upsertAll("llm_infos", &dump.LLMInfos, len(dump.LLMInfos), []clause.Column{{Name: "name"}}); err != nil {
			return err
		}

		// API keys — skip existing
		if err := cfg.doNothing("api_keys", &dump.APIKeys, len(dump.APIKeys)); err != nil {
			return err
		}

		// Users — skip existing (backward compat: might be nil in old dumps)
		if len(dump.Users) > 0 {
			if err := cfg.doNothing("users", &dump.Users, len(dump.Users)); err != nil {
				return err
			}
		}

		// Settings — upsert by key
		if err := cfg.upsertSettings(dump.Settings); err != nil {
			return err
		}

		// Notifications — skip existing
		if len(dump.Notifications) > 0 {
			if err := cfg.doNothing("notifications", &dump.Notifications, len(dump.Notifications)); err != nil {
				return err
			}
		}

		// Audit & runtime — skip existing
		if len(dump.AuditLogs) > 0 {
			if err := cfg.doNothing("audit_logs", &dump.AuditLogs, len(dump.AuditLogs)); err != nil {
				return err
			}
		}
		if len(dump.RuntimeStates) > 0 {
			if err := cfg.doNothing("auto_strategy_states", &dump.RuntimeStates, len(dump.RuntimeStates)); err != nil {
				return err
			}
		}
		if len(dump.CircuitBreakerStates) > 0 {
			if err := cfg.doNothing("circuit_breaker_states", &dump.CircuitBreakerStates, len(dump.CircuitBreakerStates)); err != nil {
				return err
			}
		}

		// Stats
		if dump.IncludeStats {
			if err := cfg.upsertAll("stats_totals", &dump.StatsTotal, len(dump.StatsTotal), []clause.Column{{Name: "id"}}); err != nil {
				return err
			}
			if err := cfg.upsertAll("stats_dailies", &dump.StatsDaily, len(dump.StatsDaily), []clause.Column{{Name: "date"}}); err != nil {
				return err
			}
			if err := cfg.upsertAll("stats_hourlies", &dump.StatsHourly, len(dump.StatsHourly), []clause.Column{{Name: "hour"}, {Name: "date"}}); err != nil {
				return err
			}
			if err := cfg.upsertAll("stats_models", &dump.StatsModel, len(dump.StatsModel), []clause.Column{{Name: "id"}}); err != nil {
				return err
			}
			if err := cfg.upsertAll("stats_channels", &dump.StatsChannel, len(dump.StatsChannel), []clause.Column{{Name: "channel_id"}}); err != nil {
				return err
			}
			if err := cfg.upsertAll("stats_api_keys", &dump.StatsAPIKey, len(dump.StatsAPIKey), []clause.Column{{Name: "api_key_id"}}); err != nil {
				return err
			}
		}

		// Relay logs, attempts, references, and blobs are restored as one family.
		// In separate-log-DB live mode this runs in the dedicated transaction below.
		if (dump.IncludeLogs || isFull) && !logToSeparateDB {
			if err := importRelayLogFamily(cfg, dump); err != nil {
				return err
			}
		}

		// API credential profiles (Tools) — skip existing
		if len(dump.APICredentialProfiles) > 0 {
			if err := cfg.doNothing("api_credential_profiles", &dump.APICredentialProfiles, len(dump.APICredentialProfiles)); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// 独立日志库 live 模式：relay_logs 在主事务外单独写入日志库。
	// 跨库非原子——主库数据已提交，日志单独导入；日志可丢，失败仅记录在结果中
	// 不回滚主库。
	//
	// 强制导入：无论「保留历史日志」开关是否开启，都把日志写入日志库。若日志库
	// 此前被 CloseLogDB 断开（用户关闭了后台日志），先 ReopenLogDB 重连——导入
	// 完成后日志库即处于开启（已连接）状态。
	if logToSeparateDB && (dump.IncludeLogs || isFull) {
		if err := db.ReopenLogDB(); err != nil {
			return nil, fmt.Errorf("reopen log db before import: %w", err)
		}
		if logConn := db.GetLogDB(); logConn != nil {
			if err := logConn.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
				logCfg := &importConfig{conn: transaction, res: res, isFull: isFull, version: dump.Version}
				return importRelayLogFamily(logCfg, dump)
			}); err != nil {
				return nil, err
			}
		}
	}

	// Summarize rows affected from progress
	for _, step := range res.Progress {
		res.RowsAffected[step.Table] += step.RowsAffected
	}
	maintenanceSucceeded = true
	return res, nil
}

// ImportIncremental is the backward-compatible wrapper.
func ImportIncremental(ctx context.Context, dump *model.DBDump) (*model.DBImportResult, error) {
	return ImportWithMode(ctx, dump, model.ImportModeIncremental)
}
