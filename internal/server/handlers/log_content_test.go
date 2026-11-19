package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
)

func TestLogContentDistinguishesEmptyMissingAndCorrupt(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "content.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(nil))
	blob := model.RelayLogContentBlob{Digest: digest, Encoding: "identity"}
	if err := db.GetLogDB().Create(&blob).Error; err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		name, state, digest string
		status              int
		message             string
	}{
		{"empty", model.RelayLogContentStateReady, digest, 200, ""},
		{"missing", model.RelayLogContentStateReady, "missing", 410, "missing"},
		{"disabled", model.RelayLogContentStateDisabled, "", 410, "disabled"},
		{"expired", model.RelayLogContentStateExpired, "", 410, "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := model.RelayLogContentRef{RelayLogID: int64(i + 1), State: tc.state, BlobDigest: tc.digest, ContentType: "text/plain"}
			if err := db.GetLogDB().Create(&ref).Error; err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/log/content?ref_id=%d", ref.ID), nil)
			logContent(c)
			if r.Code != tc.status || !strings.Contains(r.Body.String(), tc.message) {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			if tc.name == "empty" && r.Body.Len() != 0 {
				t.Fatalf("empty body became %q", r.Body.String())
			}
		})
	}
	if err := db.GetLogDB().Model(&blob).Update("payload", []byte("corrupt")).Error; err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("GET", "/api/v1/log/content?ref_id=1", nil)
	logContent(c)
	if r.Code != 500 || !strings.Contains(r.Body.String(), "corrupt") {
		t.Fatalf("corruption: %d %s", r.Code, r.Body.String())
	}
}

func TestLogPreviewHTTPAndOriginalDownload(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "preview.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("安全合成🙂", 12000)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	blob := model.RelayLogContentBlob{Digest: digest, Encoding: "identity", Payload: []byte(payload), OriginalSize: int64(len(payload)), StoredSize: int64(len(payload))}
	if err := db.GetLogDB().Create(&blob).Error; err != nil {
		t.Fatal(err)
	}
	log := model.RelayLog{ID: 76543, RequestContent: payload}
	if err := db.GetLogDB().Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	ref := model.RelayLogContentRef{RelayLogID: log.ID, Boundary: model.RelayLogBoundaryClientIngress, Kind: "body", State: model.RelayLogContentStateReady, BlobDigest: digest, ContentType: "application/json", Complete: true, CapturedBytes: int64(len(payload))}
	if err := db.GetLogDB().Create(&ref).Error; err != nil {
		t.Fatal(err)
	}
	get := func(url string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("GET", url, nil)
		handler(c)
		return r
	}
	var joined strings.Builder
	var offset int64
	for {
		r := get(fmt.Sprintf("/content?ref_id=%d&preview=true&offset=%d", ref.ID, offset), logContent)
		if r.Code != 200 || r.Body.Len() > 80<<10 {
			t.Fatalf("page status=%d bytes=%d", r.Code, r.Body.Len())
		}
		var envelope struct {
			Data relaylog.RelayLogContentPage `json:"data"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		joined.WriteString(envelope.Data.Text)
		if !envelope.Data.HasMore {
			break
		}
		offset = envelope.Data.NextOffset
	}
	if joined.String() != payload {
		t.Fatal("HTTP paging changed content")
	}
	for _, url := range []string{fmt.Sprintf("/content?ref_id=%d", ref.ID), "/content?log_id=76543&legacy=request"} {
		r := get(url, logContent)
		if r.Code != 200 || r.Body.String() != payload {
			t.Fatal("download changed content")
		}
	}
	r := get("/detail?id=76543&preview=true", logDetail)
	if r.Code != 200 || r.Body.Len() > 150<<10 {
		t.Fatalf("detail status=%d bytes=%d", r.Code, r.Body.Len())
	}
	var detail struct {
		Data model.RelayLog `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.Data.RequestContentTruncated || !detail.Data.Contents[0].TextTruncated {
		t.Fatal("partial preview not marked")
	}
	for _, query := range []string{"offset=-1", "offset=nope"} {
		if r := get(fmt.Sprintf("/content?ref_id=%d&preview=true&%s", ref.ID, query), logContent); r.Code != 400 {
			t.Fatalf("bad offset: %d", r.Code)
		}
	}
	if r := get(fmt.Sprintf("/content?ref_id=%d&preview=true&offset=%d", ref.ID, len(payload)+1), logContent); r.Code != 416 {
		t.Fatalf("range: %d", r.Code)
	}
}
