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
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// Bound both JSON parsing and browser layout. Full bytes stay in the existing
// authenticated download endpoint; preview pages never replace stored content.
const RelayLogPreviewBytes = 32 << 10

var ErrRelayLogPreviewRange = errors.New("log preview offset is outside the content")
var ErrRelayLogPreviewBinary = errors.New("log content is not UTF-8 text; download the original instead")

type RelayLogContentPage struct {
	Text       string `json:"text"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	TotalBytes int64  `json:"total_bytes"`
	HasMore    bool   `json:"has_more"`
}

func limitPreviewText(text string) (string, bool) {
	if len(text) <= RelayLogPreviewBytes {
		return text, false
	}
	end := RelayLogPreviewBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

func isClientBody(content model.RelayLogContentRef) bool {
	return content.Kind == "body" && (content.Boundary == model.RelayLogBoundaryClientIngress || content.Boundary == model.RelayLogBoundaryClientEgress)
}

func prepareCachedRelayLogPreview(log *model.RelayLog) {
	log.RequestContent, log.RequestContentTruncated = limitPreviewText(log.RequestContent)
	log.ResponseContent, log.ResponseContentTruncated = limitPreviewText(log.ResponseContent)
	for i := range log.Contents {
		content := &log.Contents[i]
		content.Text = ""
		content.TextDeferred = content.State == model.RelayLogContentStateReady && relayLogContentIsTextual(content.ContentType)
		if content.TextDeferred && (isClientBody(content.RelayLogContentRef) || content.ID == 0) {
			// Pending records have no downloadable reference yet. Keep a bounded
			// preview without copying the full payload into a second string.
			payload := content.Data
			if len(payload) > RelayLogPreviewBytes {
				end := RelayLogPreviewBytes
				for end > 0 && !utf8.RuneStart(payload[end]) {
					end--
				}
				payload = payload[:end]
				content.TextTruncated = true
			}
			if utf8.Valid(payload) {
				content.Text = string(payload)
				content.TextDeferred = false
			}
		}
		content.Data = nil
	}
}

func hydrateRelayLogPreview(ctx context.Context, connection *gorm.DB, log *model.RelayLog) error {
	var refs []model.RelayLogContentRef
	if err := connection.WithContext(ctx).Where("relay_log_id = ?", log.ID).
		Order("attempt_num ASC, boundary ASC, slot ASC, id ASC").Find(&refs).Error; err != nil {
		return err
	}
	log.Contents = make([]model.RelayLogCapturedContent, 0, len(refs))
	for _, ref := range refs {
		content := model.RelayLogCapturedContent{RelayLogContentRef: ref}
		content.TextDeferred = ref.State == model.RelayLogContentStateReady && relayLogContentIsTextual(ref.ContentType)
		// Attempts and attachments are loaded only when their own viewer opens.
		if content.TextDeferred && isClientBody(ref) {
			page, err := relayLogContentPage(ctx, connection, &content.RelayLogContentRef, 0)
			if err != nil {
				if !errors.Is(err, ErrRelayLogContentCorrupt) && !errors.Is(err, ErrRelayLogPreviewBinary) {
					return err
				}
				// A text preview failure does not change stored capture metadata.
				content.Error = err.Error()
			} else if page != nil {
				content.Text, content.TextTruncated = page.Text, page.HasMore
				content.TextDeferred = false
			}
		}
		log.Contents = append(log.Contents, content)
	}
	return nil
}

type relayLogPreviewReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r relayLogPreviewReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readRelayLogContentPage(ctx context.Context, blob *model.RelayLogContentBlob, offset int64) (*RelayLogContentPage, error) {
	if offset < 0 || offset > blob.OriginalSize {
		return nil, ErrRelayLogPreviewRange
	}
	if blob.StoredSize != int64(len(blob.Payload)) {
		return nil, fmt.Errorf("%w: stored size mismatch", ErrRelayLogContentCorrupt)
	}
	var reader io.Reader = bytes.NewReader(blob.Payload)
	switch blob.Encoding {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRelayLogContentCorrupt, err)
		}
		defer gz.Close()
		reader = gz
	default:
		return nil, fmt.Errorf("%w: unsupported encoding %q", ErrRelayLogContentCorrupt, blob.Encoding)
	}
	reader = relayLogPreviewReader{ctx: ctx, reader: reader}
	if _, err := io.CopyN(io.Discard, reader, offset); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: cannot reach offset %d: %w", ErrRelayLogContentCorrupt, offset, err)
	}
	// A few lookahead bytes distinguish EOF and let us end on a UTF-8 boundary.
	data, err := io.ReadAll(io.LimitReader(reader, RelayLogPreviewBytes+utf8.UTFMax))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrRelayLogContentCorrupt, err)
	}
	end := offset + int64(len(data))
	if end > blob.OriginalSize || (len(data) < RelayLogPreviewBytes+utf8.UTFMax && end != blob.OriginalSize) {
		return nil, fmt.Errorf("%w: decoded size does not match expected %d", ErrRelayLogContentCorrupt, blob.OriginalSize)
	}
	if offset == 0 && end == blob.OriginalSize {
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != blob.Digest {
			return nil, fmt.Errorf("%w: digest mismatch", ErrRelayLogContentCorrupt)
		}
	}
	if len(data) > RelayLogPreviewBytes {
		cut := RelayLogPreviewBytes
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		data = data[:cut]
	}
	if !utf8.Valid(data) {
		return nil, ErrRelayLogPreviewBinary
	}
	next := offset + int64(len(data))
	return &RelayLogContentPage{Text: string(data), Offset: offset, NextOffset: next, TotalBytes: blob.OriginalSize, HasMore: next < blob.OriginalSize}, nil
}

func relayLogContentPage(ctx context.Context, connection *gorm.DB, ref *model.RelayLogContentRef, offset int64) (*RelayLogContentPage, error) {
	if ref.State != model.RelayLogContentStateReady || ref.BlobDigest == "" {
		return nil, nil
	}
	if !relayLogContentIsTextual(ref.ContentType) {
		return nil, ErrRelayLogPreviewBinary
	}
	var blob model.RelayLogContentBlob
	if err := connection.WithContext(ctx).Where("digest = ?", ref.BlobDigest).First(&blob).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ref.State = model.RelayLogContentStateUnavailable
			ref.Error = "content blob is missing"
			return nil, nil
		}
		return nil, err
	}
	return readRelayLogContentPage(ctx, &blob, offset)
}

func RelayLogContentPreview(ctx context.Context, referenceID, offset int64) (*model.RelayLogContentRef, *RelayLogContentPage, error) {
	connection := db.GetLogDB()
	if connection == nil {
		return nil, nil, nil
	}
	var ref model.RelayLogContentRef
	if err := connection.WithContext(ctx).Where("id = ?", referenceID).First(&ref).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	page, err := relayLogContentPage(ctx, connection, &ref, offset)
	return &ref, page, err
}

// Legacy rows predate boundary references. Fetch only the requested column;
// never hydrate attempts just to download or page through the old body.
func RelayLogLegacyContent(ctx context.Context, id int64, field string) (string, bool, error) {
	if field != "request" && field != "response" {
		return "", false, fmt.Errorf("invalid legacy content field %q", field)
	}
	selectText := func(log *model.RelayLog) string {
		if field == "request" {
			return log.RequestContent
		}
		return log.ResponseContent
	}
	relayLogCacheLock.Lock()
	for i := range relayLogCache {
		if relayLogCache[i].ID == id {
			text := selectText(&relayLogCache[i])
			relayLogCacheLock.Unlock()
			return text, true, nil
		}
	}
	relayLogCacheLock.Unlock()
	connection := db.GetLogDB()
	if connection == nil {
		return "", false, nil
	}
	var log model.RelayLog
	if err := connection.WithContext(ctx).Select(field+"_content").Where("id = ?", id).First(&log).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return selectText(&log), true, nil
}

func RelayLogLegacyContentPage(text string, offset int64) (*RelayLogContentPage, error) {
	if offset < 0 || offset > int64(len(text)) {
		return nil, ErrRelayLogPreviewRange
	}
	preview, more := limitPreviewText(text[offset:])
	if !utf8.ValidString(preview) {
		return nil, ErrRelayLogPreviewBinary
	}
	return &RelayLogContentPage{Text: strings.Clone(preview), Offset: offset, NextOffset: offset + int64(len(preview)), TotalBytes: int64(len(text)), HasMore: more}, nil
}
