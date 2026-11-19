package relaylog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/model"
)

func TestContentPreviewPagesPreserveUTF8AndOriginal(t *testing.T) {
	for _, payload := range []string{"", "small", strings.Repeat("中文🙂abc", 20000), strings.Repeat("data: {\"delta\":\"text\"}\n\n", 4000)} {
		blob, err := buildRelayLogContentBlob([]byte(payload), 100)
		if err != nil {
			t.Fatal(err)
		}
		var result strings.Builder
		var offset int64
		for {
			page, err := readRelayLogContentPage(context.Background(), blob, offset)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Text) > RelayLogPreviewBytes || !utf8.ValidString(page.Text) {
				t.Fatal("unbounded or invalid page")
			}
			result.WriteString(page.Text)
			if !page.HasMore {
				break
			}
			if page.NextOffset <= offset {
				t.Fatal("page did not advance")
			}
			offset = page.NextOffset
		}
		if result.String() != payload {
			t.Fatal("paging lost or duplicated bytes")
		}
		full, err := decodeRelayLogContentBlob(blob)
		if err != nil || string(full) != payload {
			t.Fatal("original download changed", err)
		}
	}
}

func TestContentPreviewInvalidRangeCorruptionAndCancellation(t *testing.T) {
	blob, _ := buildRelayLogContentBlob([]byte(strings.Repeat("abcdef", 50000)), 100)
	for _, offset := range []int64{-1, blob.OriginalSize + 1} {
		if _, err := readRelayLogContentPage(context.Background(), blob, offset); !errors.Is(err, ErrRelayLogPreviewRange) {
			t.Fatalf("range: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readRelayLogContentPage(ctx, blob, 0); !errors.Is(err, context.Canceled) || errors.Is(err, ErrRelayLogContentCorrupt) {
		t.Fatalf("cancel: %v", err)
	}
	blob.StoredSize++
	if _, err := readRelayLogContentPage(context.Background(), blob, 0); !errors.Is(err, ErrRelayLogContentCorrupt) {
		t.Fatalf("size: %v", err)
	}
	small, _ := buildRelayLogContentBlob([]byte("small"), 100)
	small.Digest = "bad"
	if _, err := readRelayLogContentPage(context.Background(), small, 0); !errors.Is(err, ErrRelayLogContentCorrupt) {
		t.Fatalf("digest: %v", err)
	}
	binary, _ := buildRelayLogContentBlob([]byte{0xff, 0xfe}, 100)
	if _, err := readRelayLogContentPage(context.Background(), binary, 0); !errors.Is(err, ErrRelayLogPreviewBinary) {
		t.Fatalf("binary: %v", err)
	}
}

func TestLargeLogPreviewDefersAttemptsAndPreservesDownload(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	// Several multi-MiB bodies model image-bearing requests without private data.
	large := `{"image":"data:image/png;base64,` + strings.Repeat("abcd", 1<<20) + `"}`
	log := model.RelayLog{Time: 100, RequestContent: large, ResponseContent: "answer", Contents: []model.RelayLogCapturedContent{
		capturedText(model.RelayLogBoundaryClientIngress, 0, large),
		capturedText(model.RelayLogBoundaryUpstreamRequest, 1, large),
		capturedText(model.RelayLogBoundaryUpstreamResponse, 1, large),
		capturedText(model.RelayLogBoundaryClientEgress, 0, `{"choices":[{"message":{"content":"answer"}}]}`),
	}}
	id, err := RelayLogAdd(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}
	// Previewing a cached record must not clear or truncate its shared payload.
	preview, err := RelayLogPreviewByID(context.Background(), id)
	if err != nil || preview == nil {
		t.Fatal(err)
	}
	full, err := RelayLogGetByID(context.Background(), id)
	if err != nil || len(full.Contents[0].Data) != len(large) {
		t.Fatal("cache was mutated", err)
	}
	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Remove the cache so this assertion covers database projection/hydration.
	restore := SetCacheForTest(nil)
	defer restore()
	preview, err = RelayLogPreviewByID(context.Background(), id)
	if err != nil || preview == nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 150<<10 {
		t.Fatalf("preview response grew to %d bytes", len(encoded))
	}
	var requestRef int64
	for _, content := range preview.Contents {
		if content.Boundary == model.RelayLogBoundaryUpstreamRequest || content.Boundary == model.RelayLogBoundaryUpstreamResponse {
			if !content.TextDeferred || content.Text != "" {
				t.Fatal("attempt body eagerly loaded")
			}
		}
		if content.Boundary == model.RelayLogBoundaryClientIngress {
			requestRef = content.ID
			if !content.TextTruncated || !content.Complete {
				t.Fatal("preview/capture completeness conflated")
			}
		}
	}
	_, payload, err := RelayLogContentGet(context.Background(), requestRef)
	if err != nil || string(payload) != large {
		t.Fatal("full content lost", err)
	}
	// Deferring an attempt does not even read its blob (a missing blob is only
	// reported when explicitly requested, not while opening another boundary).
	var ref model.RelayLogContentRef
	if err := connection.First(&ref, "id = ?", requestRef).Error; err != nil {
		t.Fatal(err)
	}
	pageRef, page, err := RelayLogContentPreview(context.Background(), requestRef, 0)
	if err != nil || pageRef == nil || page == nil || !page.HasMore {
		t.Fatal("explicit preview missing", err)
	}
}

func TestLegacyPreviewRetainsFullReadAndUTF8(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	large := strings.Repeat("正文🙂", 50000)
	log := model.RelayLog{ID: 34321, Time: 100, RequestContent: large, ResponseContent: ""}
	if err := connection.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := RelayLogPreviewByID(context.Background(), log.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.RequestContentTruncated || preview.ResponseContentTruncated || len(preview.RequestContent) > RelayLogPreviewBytes || !utf8.ValidString(preview.RequestContent) {
		t.Fatal("invalid legacy projection")
	}
	text, found, err := RelayLogLegacyContent(context.Background(), log.ID, "request")
	if err != nil || !found || text != large {
		t.Fatal("legacy download lost", err)
	}
	page, err := RelayLogLegacyContentPage(text, 0)
	if err != nil || !page.HasMore {
		t.Fatal(err)
	}
	if _, _, err := RelayLogLegacyContent(context.Background(), log.ID, "id; DELETE"); err == nil {
		t.Fatal("invalid field accepted")
	}
}
