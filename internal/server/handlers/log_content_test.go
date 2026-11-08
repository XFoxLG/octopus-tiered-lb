package handlers

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
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
