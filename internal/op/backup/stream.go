package backup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
	"gorm.io/gorm"
)

// CreateJSONExportFile writes a complete backup to a temporary file without
// retaining relay bodies and attachments in the Go heap. The caller owns and
// must close and remove the returned file.
func CreateJSONExportFile(ctx context.Context, includeLogs bool, includeStats bool, excludeUsers bool) (*os.File, int64, error) {
	temporaryFile, err := os.CreateTemp("", "octopus-backup-*.json")
	if err != nil {
		return nil, 0, fmt.Errorf("create backup file: %w", err)
	}
	cleanup := func() {
		_ = temporaryFile.Close()
		_ = os.Remove(temporaryFile.Name())
	}
	if err := WriteJSON(ctx, temporaryFile, includeLogs, includeStats, excludeUsers); err != nil {
		cleanup()
		return nil, 0, err
	}
	fileInfo, err := temporaryFile.Stat()
	if err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("stat backup file: %w", err)
	}
	if _, err := temporaryFile.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("rewind backup file: %w", err)
	}
	return temporaryFile, fileInfo.Size(), nil
}

// WriteJSON streams large log-family arrays while keeping the existing
// versioned DBDump object shape. Core configuration remains small enough to
// marshal normally; audit logs, relay parents, attempts, refs, and blobs are
// read row-by-row from their owning database.
func WriteJSON(ctx context.Context, destination io.Writer, includeLogs bool, includeStats bool, excludeUsers bool) error {
	if destination == nil {
		return fmt.Errorf("backup destination is nil")
	}
	baseDump, err := ExportAll(ctx, false, includeStats)
	if err != nil {
		return err
	}
	baseDump.IncludeLogs = includeLogs
	if excludeUsers {
		baseDump.Users = nil
	}
	baseJSON, err := utilsjson.Marshal(baseDump)
	if err != nil {
		return fmt.Errorf("marshal backup metadata: %w", err)
	}
	if !includeLogs {
		_, err = destination.Write(baseJSON)
		return err
	}
	if len(baseJSON) == 0 || baseJSON[len(baseJSON)-1] != '}' {
		return fmt.Errorf("backup metadata is not a JSON object")
	}

	if db.IsLogDBSeparate() {
		if err := db.ReopenLogDB(); err != nil {
			return fmt.Errorf("reopen log db before export: %w", err)
		}
	}
	logConnection := db.GetLogDB()
	if logConnection == nil {
		return fmt.Errorf("log database is unavailable")
	}

	bufferedWriter := bufio.NewWriterSize(destination, 128*1024)
	if _, err := bufferedWriter.Write(baseJSON[:len(baseJSON)-1]); err != nil {
		return err
	}

	mainConnection := db.GetDB().WithContext(ctx)
	if err := writeGORMJSONArray(bufferedWriter, "audit_logs", mainConnection.Model(&model.AuditLog{}).Order("id DESC").Limit(maxAuditLogsExport), mainConnection, func(row model.AuditLog) any {
		return row
	}); err != nil {
		return fmt.Errorf("stream audit_logs: %w", err)
	}

	logConnection = logConnection.WithContext(ctx)
	selectedLogIDs := logConnection.Model(&model.RelayLog{}).
		Select("id").
		Order("id DESC").
		Limit(maxRelayLogsExport)
	if err := writeGORMJSONArray(bufferedWriter, "relay_logs", logConnection.Model(&model.RelayLog{}).Order("id DESC").Limit(maxRelayLogsExport), logConnection, func(row model.RelayLog) any {
		return row
	}); err != nil {
		return fmt.Errorf("stream relay_logs: %w", err)
	}
	if err := writeGORMJSONArray(bufferedWriter, "relay_log_attempts", logConnection.Model(&model.RelayLogAttempt{}).
		Where("relay_log_id IN (?)", selectedLogIDs).
		Order("relay_log_id ASC, attempt_num ASC, id ASC"), logConnection, func(row model.RelayLogAttempt) any {
		return row
	}); err != nil {
		return fmt.Errorf("stream relay_log_attempts: %w", err)
	}
	if err := writeGORMJSONArray(bufferedWriter, "relay_log_content_refs", logConnection.Model(&model.RelayLogContentRef{}).
		Where("relay_log_id IN (?)", selectedLogIDs).
		Order("relay_log_id ASC, attempt_num ASC, boundary ASC, slot ASC, id ASC"), logConnection, func(row model.RelayLogContentRef) any {
		return row
	}); err != nil {
		return fmt.Errorf("stream relay_log_content_refs: %w", err)
	}
	reachableBlobDigests := logConnection.Model(&model.RelayLogContentRef{}).
		Distinct("blob_digest").
		Where("relay_log_id IN (?) AND blob_digest <> ''", selectedLogIDs)
	if err := writeGORMJSONArray(bufferedWriter, "relay_log_content_blobs", logConnection.Model(&model.RelayLogContentBlob{}).
		Where("digest IN (?)", reachableBlobDigests).
		Order("digest ASC"), logConnection, func(row model.RelayLogContentBlob) any {
		return model.RelayLogContentBlobDump{
			Digest:       row.Digest,
			Encoding:     row.Encoding,
			OriginalSize: row.OriginalSize,
			StoredSize:   row.StoredSize,
			Payload:      row.Payload,
			CreatedAt:    row.CreatedAt,
		}
	}); err != nil {
		return fmt.Errorf("stream relay_log_content_blobs: %w", err)
	}
	if err := bufferedWriter.WriteByte('}'); err != nil {
		return err
	}
	return bufferedWriter.Flush()
}

func writeGORMJSONArray[T any](writer *bufio.Writer, fieldName string, query *gorm.DB, scanConnection *gorm.DB, transform func(T) any) error {
	if writer == nil || query == nil || scanConnection == nil {
		return fmt.Errorf("invalid streaming query arguments")
	}
	fieldJSON, err := utilsjson.Marshal(fieldName)
	if err != nil {
		return err
	}
	if err := writer.WriteByte(','); err != nil {
		return err
	}
	if _, err := writer.Write(fieldJSON); err != nil {
		return err
	}
	if _, err := writer.WriteString(`:[`); err != nil {
		return err
	}

	rows, err := query.Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	firstRow := true
	for rows.Next() {
		var row T
		if err := scanConnection.ScanRows(rows, &row); err != nil {
			return err
		}
		rowJSON, err := utilsjson.Marshal(transform(row))
		if err != nil {
			return err
		}
		if !firstRow {
			if err := writer.WriteByte(','); err != nil {
				return err
			}
		}
		firstRow = false
		if _, err := writer.Write(rowJSON); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writer.WriteByte(']')
}
