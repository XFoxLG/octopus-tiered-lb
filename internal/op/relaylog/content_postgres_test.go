package relaylog

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	jsoniter "github.com/json-iterator/go"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func relayLogPostgresFixture(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("OCTOPUS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL test database not configured")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		t.Fatal("test PostgreSQL must be local")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := fmt.Sprintf("relaylog_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	conn, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := conn.AutoMigrate(&model.RelayLog{}, &model.RelayLogAttempt{}, &model.RelayLogContentBlob{}, &model.RelayLogContentRef{}); err != nil {
		t.Fatal(err)
	}
	return conn, u.String()
}

// jsoniter's integer decoder can put an actual 0xff into an error string.
// JSON detail responses replace it with U+FFFD, hiding the byte that PostgreSQL
// rejects in the separate attempt row's text column.
func legacyGeminiDecodeError(t *testing.T) string {
	t.Helper()
	var oldEnvelope struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	err := jsoniter.Unmarshal([]byte(`{"error":{"message":"fixture","code":"no_credential"}}`), &oldEnvelope)
	if err == nil {
		t.Fatal("old numeric-only decoder should reject symbolic code")
	}
	if utf8.ValidString(err.Error()) || !strings.Contains(err.Error(), "\xff") {
		t.Fatalf("fixture did not produce the observed invalid byte: %q", err)
	}
	return err.Error()
}

func TestRelayLogPostgresInvalidErrorText(t *testing.T) {
	conn, dsn := relayLogPostgresFixture(t)
	message := legacyGeminiDecodeError(t)
	binary := []byte{0, 0xff, 0xfe, 0x80, 1}
	compressed := bytes.Repeat(binary, 600)
	rows := []model.RelayLog{
		{ID: 101, Time: 1, Attempts: []model.ChannelAttempt{{ChannelID: 1, AttemptNum: 1, Status: model.AttemptFailed, Msg: message}}, Contents: []model.RelayLogCapturedContent{capturedText(model.RelayLogBoundaryUpstreamResponse, 1, strings.Repeat("safe-body", 300))}},
		{ID: 102, Time: 1, Contents: []model.RelayLogCapturedContent{capturedText(model.RelayLogBoundaryClientEgress, 0, `{"data":[]}`)}},
		{ID: 103, Time: 1, Error: "fixture\x00\xff", RequestContent: "legacy\x00\xff", Contents: []model.RelayLogCapturedContent{capturedText(model.RelayLogBoundaryUpstreamResponse, 0, string(binary))}},
		{ID: 104, Time: 1, Contents: []model.RelayLogCapturedContent{capturedText(model.RelayLogBoundaryUpstreamResponse, 0, string(compressed))}},
	}
	rows[2].Contents[0].FileName = "fixture\xff.bin"
	for i := range rows {
		prepareRelayLogContentForQueue(&rows[i], 0, false)
	}
	if err := persistRelayLogBatch(context.Background(), conn, rows); err != nil {
		t.Fatalf("mixed batch blocked by error text: %v", err)
	}
	// Retrying the same batch stays idempotent and never alters captured bytes.
	if err := persistRelayLogBatch(context.Background(), conn, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Attempts[0].Msg != message || rows[2].Error != "fixture\x00\xff" || !bytes.Equal(rows[2].Contents[0].Data, binary) {
		t.Fatal("database presentation mutated the queued source")
	}
	// Read through a fresh pool, with no cached in-memory request/response data.
	sqlDB, _ := conn.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	reopenedSQL, _ := conn.DB()
	defer reopenedSQL.Close()
	var count int64
	if err := conn.Model(&model.RelayLog{}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("persisted rows=%d err=%v", count, err)
	}
	var attempt model.RelayLogAttempt
	if err := conn.Where("relay_log_id = ?", 101).First(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(attempt.Msg, `\xff`) || !strings.Contains(attempt.Msg, "no_credent") || !utf8.ValidString(attempt.Msg) {
		t.Fatalf("provider evidence lost: %q", attempt.Msg)
	}
	var parent model.RelayLog
	if err := conn.First(&parent, 101).Error; err != nil {
		t.Fatal(err)
	}
	if len(parent.Attempts) != 1 || parent.Attempts[0].Msg != attempt.Msg {
		t.Fatal("parent and attempt evidence diverged")
	}
	if err := conn.First(&model.RelayLog{}, 102).Error; err != nil {
		t.Fatalf("healthy neighbor unavailable: %v", err)
	}
	for i, row := range rows {
		var persisted model.RelayLog
		if err := conn.First(&persisted, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.PersistenceState != model.RelayLogPersistenceReady || persisted.ContentState != model.RelayLogContentStateReady {
			t.Fatalf("row %d not ready", row.ID)
		}
		var refs []model.RelayLogContentRef
		if err := conn.Where("relay_log_id = ?", row.ID).Find(&refs).Error; err != nil {
			t.Fatal(err)
		}
		if len(refs) != 1 || refs[0].State != model.RelayLogContentStateReady {
			t.Fatalf("unexpected references for row %d: %d", row.ID, len(refs))
		}
		var blob model.RelayLogContentBlob
		if err := conn.Where("digest = ?", refs[0].BlobDigest).First(&blob).Error; err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeRelayLogContentBlob(&blob)
		if err != nil || !bytes.Equal(decoded, row.Contents[0].Data) {
			t.Fatalf("row %d bytes changed: %v", row.ID, err)
		}
		if i == 2 && (persisted.Error != `fixture\x00\xff` || refs[0].FileName != `fixture\xff.bin` || blob.Encoding != "identity") {
			t.Fatal("invalid metadata or identity binary not retained")
		}
		if i == 3 && blob.Encoding != "gzip" {
			t.Fatal("fixture did not exercise compressed binary")
		}
	}
}
