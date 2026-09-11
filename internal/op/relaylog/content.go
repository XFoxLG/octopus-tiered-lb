package relaylog

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// The queue owns complete bundles, never partial prefixes. When this budget
	// is exhausted, metadata is retained and the whole new bundle is marked
	// unavailable instead of allowing unbounded heap growth.
	relayLogPendingContentMaxBytes int64 = 64 << 20

	// Content storage deliberately uses only part of the 1 GiB Aiven Free disk.
	// Operators can override this through relay_log_content_keep_size_mb.
	defaultRelayLogContentKeepSizeMB = 256
)

func relayLogCapturedPayloadBytes(relayLog *model.RelayLog) int64 {
	if relayLog == nil {
		return 0
	}
	total := int64(len(relayLog.RequestContent) + len(relayLog.ResponseContent))
	for i := range relayLog.Contents {
		total += int64(len(relayLog.Contents[i].Data))
	}
	return total
}

func releaseRelayLogCapturedPayloads(relayLog *model.RelayLog) {
	if relayLog == nil {
		return
	}
	relayLog.RequestContent = ""
	relayLog.ResponseContent = ""
	for i := range relayLog.Contents {
		relayLog.Contents[i].Data = nil
		relayLog.Contents[i].Text = ""
	}
}

func markRelayLogContentUnavailable(relayLog *model.RelayLog, reason string) {
	if relayLog == nil {
		return
	}
	relayLogUnavailableContentBundles.Add(1)
	relayLog.ContentState = model.RelayLogContentStateUnavailable
	relayLog.ContentUnavailableError = reason
	relayLog.RequestContent = ""
	relayLog.ResponseContent = ""
	for index := range relayLog.Contents {
		relayLog.Contents[index].State = model.RelayLogContentStateUnavailable
		relayLog.Contents[index].Error = reason
		relayLog.Contents[index].BlobDigest = ""
		relayLog.Contents[index].Data = nil
		relayLog.Contents[index].Text = ""
	}
}

func prepareRelayLogContentForQueue(relayLog *model.RelayLog, pendingBytes int64) int64 {
	if relayLog == nil {
		return 0
	}

	payloadBytes := relayLogCapturedPayloadBytes(relayLog)
	if len(relayLog.Contents) == 0 {
		if payloadBytes == 0 && relayLog.ContentState == "" {
			relayLog.ContentState = model.RelayLogContentStateDisabled
		}
		if payloadBytes == 0 {
			return 0
		}
		if payloadBytes > relayLogPendingContentMaxBytes || pendingBytes+payloadBytes > relayLogPendingContentMaxBytes {
			markRelayLogContentUnavailable(relayLog, "content queue byte budget exceeded")
			return 0
		}
		if relayLog.ContentState == "" {
			relayLog.ContentState = model.RelayLogContentStatePending
		}
		return payloadBytes
	}

	if payloadBytes > relayLogPendingContentMaxBytes || pendingBytes+payloadBytes > relayLogPendingContentMaxBytes {
		markRelayLogContentUnavailable(relayLog, "content queue byte budget exceeded")
		return 0
	}

	hasUnavailableContent := false
	hasReadyContent := false
	for index := range relayLog.Contents {
		content := &relayLog.Contents[index]
		content.RelayLogID = relayLog.ID
		if content.CreatedAt == 0 {
			content.CreatedAt = relayLog.Time
		}
		if content.State == "" {
			if content.Data != nil {
				content.State = model.RelayLogContentStateReady
			} else {
				content.State = model.RelayLogContentStateUnavailable
			}
		}
		switch content.State {
		case model.RelayLogContentStateReady:
			hasReadyContent = true
		case model.RelayLogContentStateUnavailable:
			hasUnavailableContent = true
		}
	}

	switch {
	case hasUnavailableContent:
		relayLog.ContentState = model.RelayLogContentStateUnavailable
	case hasReadyContent:
		relayLog.ContentState = model.RelayLogContentStatePending
	default:
		relayLog.ContentState = model.RelayLogContentStateDisabled
	}
	return payloadBytes
}

func relayLogContentAttempts(relayLogs []model.RelayLog) []model.RelayLogAttempt {
	rows := make([]model.RelayLogAttempt, 0)
	for relayLogIndex := range relayLogs {
		relayLog := &relayLogs[relayLogIndex]
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

func persistRelayLogBatch(ctx context.Context, connection *gorm.DB, relayLogs []model.RelayLog) error {
	if connection == nil || len(relayLogs) == 0 {
		return nil
	}

	return connection.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		for relayLogIndex := range relayLogs {
			relayLogs[relayLogIndex].PersistenceState = model.RelayLogPersistencePending
		}
		if err := transaction.Clauses(clause.OnConflict{DoNothing: true}).Create(&relayLogs).Error; err != nil {
			return err
		}

		relayLogIDs := make([]int64, 0, len(relayLogs))
		for relayLogIndex := range relayLogs {
			relayLogIDs = append(relayLogIDs, relayLogs[relayLogIndex].ID)
		}

		// Delete-and-recreate makes retries idempotent and keeps parent, attempts,
		// and boundary references inside one transaction.
		if err := transaction.Where("relay_log_id IN ?", relayLogIDs).Delete(&model.RelayLogAttempt{}).Error; err != nil {
			return err
		}
		attemptRows := relayLogContentAttempts(relayLogs)
		if len(attemptRows) > 0 {
			if err := transaction.Create(&attemptRows).Error; err != nil {
				return err
			}
		}

		if err := transaction.Where("relay_log_id IN ?", relayLogIDs).Delete(&model.RelayLogContentRef{}).Error; err != nil {
			return err
		}
		for relayLogIndex := range relayLogs {
			relayLog := &relayLogs[relayLogIndex]
			contentState := relayLog.ContentState
			for contentIndex := range relayLog.Contents {
				capturedContent := &relayLog.Contents[contentIndex]
				capturedContent.RelayLogID = relayLog.ID
				contentRef := capturedContent.RelayLogContentRef

				if contentRef.State == model.RelayLogContentStateReady && capturedContent.Data != nil {
					blob, encodeErr := buildRelayLogContentBlob(capturedContent.Data, capturedContent.CreatedAt)
					if encodeErr != nil {
						contentRef.State = model.RelayLogContentStateUnavailable
						contentRef.Error = encodeErr.Error()
						contentState = model.RelayLogContentStateUnavailable
					} else {
						if err := transaction.Clauses(clause.OnConflict{DoNothing: true}).Create(blob).Error; err != nil {
							return err
						}
						contentRef.BlobDigest = blob.Digest
					}
				}

				if err := transaction.Create(&contentRef).Error; err != nil {
					return err
				}
			}

			if contentState == model.RelayLogContentStatePending {
				contentState = model.RelayLogContentStateReady
			}
			if err := transaction.Model(&model.RelayLog{}).
				Where("id = ?", relayLog.ID).
				Updates(map[string]any{
					"content_state":             contentState,
					"content_unavailable_error": relayLog.ContentUnavailableError,
					"persistence_state":         model.RelayLogPersistenceReady,
				}).Error; err != nil {
				return err
			}
		}

		return deleteUnreferencedRelayLogContentBlobs(transaction)
	})
}

func persistUnavailableRelayLogMetadata(ctx context.Context, relayLog model.RelayLog, reason string) {
	connection := db.GetLogDB()
	if connection == nil {
		return
	}
	relayLog.RequestContent = ""
	relayLog.ResponseContent = ""
	relayLog.Contents = nil
	relayLog.ContentState = model.RelayLogContentStateUnavailable
	relayLog.ContentUnavailableError = reason
	relayLog.PersistenceState = model.RelayLogPersistenceUnavailable
	if err := connection.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&relayLog).Error; err != nil {
		relayLogPersistenceFailures.Add(1)
		relayLogLastPersistenceFailureAt.Store(time.Now().Unix())
		relayLogLastPersistenceError.Store(err.Error())
	}
}

func buildRelayLogContentBlob(payload []byte, createdAt int64) (*model.RelayLogContentBlob, error) {
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	storedPayload := payload
	encoding := "identity"

	if len(payload) >= 1024 {
		var compressed bytes.Buffer
		writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(payload); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if compressed.Len() < len(payload) {
			storedPayload = append([]byte(nil), compressed.Bytes()...)
			encoding = "gzip"
		}
	}

	return &model.RelayLogContentBlob{
		Digest:       digest,
		Encoding:     encoding,
		OriginalSize: int64(len(payload)),
		StoredSize:   int64(len(storedPayload)),
		Payload:      append([]byte(nil), storedPayload...),
		CreatedAt:    createdAt,
	}, nil
}

func decodeRelayLogContentBlob(blob *model.RelayLogContentBlob) ([]byte, error) {
	if blob == nil {
		return nil, errors.New("content blob is nil")
	}
	if blob.StoredSize != int64(len(blob.Payload)) {
		return nil, fmt.Errorf("stored content size %d does not match payload size %d", blob.StoredSize, len(blob.Payload))
	}
	var decoded []byte
	switch blob.Encoding {
	case "", "identity":
		decoded = append([]byte(nil), blob.Payload...)
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(blob.Payload))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		decoded, err = io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported relay log content encoding %q", blob.Encoding)
	}
	if blob.OriginalSize != int64(len(decoded)) {
		return nil, fmt.Errorf("decoded content size %d does not match expected %d", len(decoded), blob.OriginalSize)
	}
	digestBytes := sha256.Sum256(decoded)
	if actualDigest := hex.EncodeToString(digestBytes[:]); actualDigest != blob.Digest {
		return nil, fmt.Errorf("content digest mismatch: expected %s, got %s", blob.Digest, actualDigest)
	}
	return decoded, nil
}

func hydrateRelayLogContents(ctx context.Context, connection *gorm.DB, relayLog *model.RelayLog) error {
	if connection == nil || relayLog == nil {
		return nil
	}

	var contentRefs []model.RelayLogContentRef
	if err := connection.WithContext(ctx).
		Where("relay_log_id = ?", relayLog.ID).
		Order("attempt_num ASC, boundary ASC, slot ASC, id ASC").
		Find(&contentRefs).Error; err != nil {
		return err
	}

	relayLog.Contents = make([]model.RelayLogCapturedContent, 0, len(contentRefs))
	for _, contentRef := range contentRefs {
		capturedContent := model.RelayLogCapturedContent{RelayLogContentRef: contentRef}
		if contentRef.State == model.RelayLogContentStateReady && contentRef.BlobDigest != "" && relayLogContentIsTextual(contentRef.ContentType) {
			var blob model.RelayLogContentBlob
			if err := connection.WithContext(ctx).Where("digest = ?", contentRef.BlobDigest).First(&blob).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					capturedContent.State = model.RelayLogContentStateUnavailable
					capturedContent.Error = "content blob is missing"
				} else {
					return err
				}
			} else if decoded, err := decodeRelayLogContentBlob(&blob); err != nil {
				capturedContent.State = model.RelayLogContentStateUnavailable
				capturedContent.Error = err.Error()
			} else if utf8.Valid(decoded) {
				capturedContent.Text = string(decoded)
			}
		}
		relayLog.Contents = append(relayLog.Contents, capturedContent)
	}
	return nil
}

func hydrateCachedRelayLogContents(relayLog *model.RelayLog) {
	if relayLog == nil {
		return
	}
	for contentIndex := range relayLog.Contents {
		content := &relayLog.Contents[contentIndex]
		if content.State == model.RelayLogContentStateReady && relayLogContentIsTextual(content.ContentType) && utf8.Valid(content.Data) {
			content.Text = string(content.Data)
		}
	}
}

func relayLogContentIsTextual(contentType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return normalized == "" ||
		strings.HasPrefix(normalized, "text/") ||
		strings.Contains(normalized, "json") ||
		strings.Contains(normalized, "xml") ||
		strings.Contains(normalized, "javascript") ||
		strings.Contains(normalized, "yaml")
}

func RelayLogContentGet(ctx context.Context, referenceID int64) (*model.RelayLogContentRef, []byte, error) {
	connection := db.GetLogDB()
	if connection == nil {
		return nil, nil, nil
	}

	var contentRef model.RelayLogContentRef
	if err := connection.WithContext(ctx).Where("id = ?", referenceID).First(&contentRef).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if contentRef.State != model.RelayLogContentStateReady || contentRef.BlobDigest == "" {
		return &contentRef, nil, nil
	}

	var blob model.RelayLogContentBlob
	if err := connection.WithContext(ctx).Where("digest = ?", contentRef.BlobDigest).First(&blob).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &contentRef, nil, nil
		}
		return nil, nil, err
	}
	payload, err := decodeRelayLogContentBlob(&blob)
	if err != nil {
		return nil, nil, err
	}
	return &contentRef, payload, nil
}

func deleteRelayLogContentsForIDs(transaction *gorm.DB, relayLogIDs []int64, nextState string) error {
	if transaction == nil || len(relayLogIDs) == 0 {
		return nil
	}
	if err := transaction.Where("relay_log_id IN ?", relayLogIDs).Delete(&model.RelayLogContentRef{}).Error; err != nil {
		return err
	}
	if nextState != "" {
		if err := transaction.Model(&model.RelayLog{}).
			Where("id IN ?", relayLogIDs).
			Updates(map[string]any{
				"content_state":             nextState,
				"content_unavailable_error": "",
				"request_content":           "",
				"response_content":          "",
			}).Error; err != nil {
			return err
		}
	}
	return deleteUnreferencedRelayLogContentBlobs(transaction)
}

func deleteUnreferencedRelayLogContentBlobs(transaction *gorm.DB) error {
	if transaction == nil {
		return nil
	}
	return transaction.Where(
		"NOT EXISTS (?)",
		transaction.Model(&model.RelayLogContentRef{}).
			Select("1").
			Where("relay_log_content_refs.blob_digest = relay_log_content_blobs.digest"),
	).Delete(&model.RelayLogContentBlob{}).Error
}

func getRelayLogContentKeepSizeMB() int {
	value, err := setting.GetInt(model.SettingKeyRelayLogContentKeepSizeMB)
	if err != nil || value < 0 {
		return defaultRelayLogContentKeepSizeMB
	}
	return value
}

func getRelayLogContentKeepPeriod() int {
	value, err := setting.GetInt(model.SettingKeyRelayLogContentKeepPeriod)
	if err != nil || value < 0 {
		return 7
	}
	return value
}

func relayLogContentStorageUsage(ctx context.Context, connection *gorm.DB) (logicalBytes int64, physicalBytes int64, physicalAvailable bool) {
	if connection == nil {
		return 0, 0, false
	}
	_ = connection.WithContext(ctx).Model(&model.RelayLogContentBlob{}).
		Select("COALESCE(SUM(stored_size), 0)").
		Scan(&logicalBytes).Error
	if connection.Dialector.Name() != "postgres" {
		return logicalBytes, 0, false
	}
	physicalQuery := `SELECT
		COALESCE(pg_total_relation_size(to_regclass('relay_log_content_blobs')), 0) +
		COALESCE(pg_total_relation_size(to_regclass('relay_log_content_refs')), 0)`
	if err := connection.WithContext(ctx).Raw(physicalQuery).Scan(&physicalBytes).Error; err != nil {
		return logicalBytes, 0, false
	}
	return logicalBytes, physicalBytes, true
}

func expireRelayLogContentByAge(ctx context.Context, connection *gorm.DB) error {
	if connection == nil {
		return nil
	}
	keepPeriod := getRelayLogContentKeepPeriod()
	if keepPeriod == 0 {
		return nil
	}
	cutoffTime := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	for {
		var relayLogIDs []int64
		if err := connection.WithContext(ctx).Model(&model.RelayLog{}).
			Distinct("relay_logs.id").
			Joins("JOIN relay_log_content_refs ON relay_log_content_refs.relay_log_id = relay_logs.id").
			Where("relay_logs.time < ?", cutoffTime).
			Order("relay_logs.time ASC, relay_logs.id ASC").
			Limit(100).
			Pluck("relay_logs.id", &relayLogIDs).Error; err != nil {
			return err
		}
		if len(relayLogIDs) == 0 {
			return nil
		}
		if err := connection.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return deleteRelayLogContentsForIDs(transaction, relayLogIDs, model.RelayLogContentStateExpired)
		}); err != nil {
			return err
		}
	}
}

func expireRelayLogContentBySize(ctx context.Context, connection *gorm.DB) error {
	if connection == nil {
		return nil
	}
	keepSizeMB := getRelayLogContentKeepSizeMB()
	if keepSizeMB == 0 {
		return nil
	}
	maximumStoredBytes := int64(keepSizeMB) * 1024 * 1024

	for {
		var storedBytes int64
		if err := connection.WithContext(ctx).Model(&model.RelayLogContentBlob{}).
			Select("COALESCE(SUM(stored_size), 0)").
			Scan(&storedBytes).Error; err != nil {
			return err
		}
		if storedBytes <= maximumStoredBytes {
			return nil
		}

		var oldestRelayLogIDs []int64
		if err := connection.WithContext(ctx).Model(&model.RelayLog{}).
			Distinct("relay_logs.id").
			Joins("JOIN relay_log_content_refs ON relay_log_content_refs.relay_log_id = relay_logs.id").
			Order("relay_logs.time ASC, relay_logs.id ASC").
			Limit(1).
			Pluck("relay_logs.id", &oldestRelayLogIDs).Error; err != nil {
			return err
		}
		if len(oldestRelayLogIDs) == 0 {
			return nil
		}

		if err := connection.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return deleteRelayLogContentsForIDs(transaction, oldestRelayLogIDs, model.RelayLogContentStateExpired)
		}); err != nil {
			return err
		}
	}
}

func maintainRelayLogContent(ctx context.Context, connection *gorm.DB) error {
	relayLogFlushLock.Lock()
	defer relayLogFlushLock.Unlock()
	if err := expireRelayLogContentByAge(ctx, connection); err != nil {
		return err
	}
	return expireRelayLogContentBySize(ctx, connection)
}

func clearAllRelayLogContentTables(ctx context.Context, connection *gorm.DB) error {
	if connection == nil {
		return nil
	}
	if err := db.FastClearTable(connection.WithContext(ctx), &model.RelayLogContentRef{}, "relay_log_content_refs"); err != nil {
		return err
	}
	return db.FastClearTable(connection.WithContext(ctx), &model.RelayLogContentBlob{}, "relay_log_content_blobs")
}

func relayLogContentCreatedAt() int64 {
	return time.Now().Unix()
}
