package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddChannelUpstreamProtocols(t *testing.T) {
	dbPath := t.TempDir() + "/channel-068.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 从旧 schema 起步（只有旧列，没有新列），验证迁移真正加列并平移数据。
	if err := db.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY,
		name TEXT,
		outbound_format_override TEXT NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	legacyRows := []struct {
		id       int
		name     string
		override string
	}{
		{1, "chat-only-station", "chat_only"},
		{2, "responses-only-station", "responses_only"},
		{3, "follow-group", ""},
		{4, "already-migrated", "chat_only"},
	}
	for _, row := range legacyRows {
		if err := db.Exec(
			"INSERT INTO channels (id, name, outbound_format_override) VALUES (?, ?, ?)",
			row.id, row.name, row.override,
		).Error; err != nil {
			t.Fatalf("insert legacy channel %d: %v", row.id, err)
		}
	}

	if err := addChannelUpstreamProtocols(db); err != nil {
		t.Fatalf("addChannelUpstreamProtocols: %v", err)
	}
	// 幂等：重复执行不报错，也不会覆盖已写入的新值。
	if err := addChannelUpstreamProtocols(db); err != nil {
		t.Fatalf("addChannelUpstreamProtocols idempotent: %v", err)
	}

	for _, col := range []string{
		"upstream_protocols",
		"first_token_time_out",
		"attempt_time_out",
		"stream_idle_timeout",
		"reasoning_buffer_strategy",
	} {
		if !db.Migrator().HasColumn(&model.Channel{}, col) {
			t.Fatalf("channels missing column %s", col)
		}
	}

	type storedRow struct {
		ID                     int
		UpstreamProtocols      string
		FirstTokenTimeOut      int
		AttemptTimeOut         int
		StreamIdleTimeout      int
		ReasoningBufferStrategy string
	}
	readRow := func(id int) storedRow {
		var row storedRow
		if err := db.Table("channels").
			Select("id", "upstream_protocols", "first_token_time_out", "attempt_time_out", "stream_idle_timeout", "reasoning_buffer_strategy").
			Where("id = ?", id).
			First(&row).Error; err != nil {
			t.Fatalf("query channel %d: %v", id, err)
		}
		return row
	}

	if got := readRow(1); got.UpstreamProtocols != `["chat_only"]` {
		t.Fatalf("channel 1 upstream_protocols = %q, want [\"chat_only\"]", got.UpstreamProtocols)
	}
	if got := readRow(2); got.UpstreamProtocols != `["responses_only"]` {
		t.Fatalf("channel 2 upstream_protocols = %q, want [\"responses_only\"]", got.UpstreamProtocols)
	}
	// 旧值为空的行必须保持未声明（= 沿用分组），不能被写成 "[]" 或 "null"。
	if got := readRow(3); got.UpstreamProtocols != "" {
		t.Fatalf("channel 3 upstream_protocols = %q, want empty", got.UpstreamProtocols)
	}
	if got := readRow(4); got.UpstreamProtocols != `["chat_only"]` {
		t.Fatalf("channel 4 upstream_protocols = %q, want [\"chat_only\"]", got.UpstreamProtocols)
	}

	// 超时三项默认 0（= 跟随分组），保证存量渠道升级后行为不变。
	for _, id := range []int{1, 2, 3, 4} {
		row := readRow(id)
		if row.FirstTokenTimeOut != 0 || row.AttemptTimeOut != 0 || row.StreamIdleTimeout != 0 {
			t.Fatalf("channel %d timeout defaults = (%d, %d, %d), want all 0 (follow group)",
				id, row.FirstTokenTimeOut, row.AttemptTimeOut, row.StreamIdleTimeout)
		}
		if row.ReasoningBufferStrategy != "" {
			t.Fatalf("channel %d reasoning_buffer_strategy = %q, want empty", id, row.ReasoningBufferStrategy)
		}
	}
}

// 回填必须只写「新列为空」的行，不能覆盖用户已经显式设置过的协议声明。
func TestAddChannelUpstreamProtocolsDoesNotOverwriteExistingDeclarations(t *testing.T) {
	dbPath := t.TempDir() + "/channel-068-preserve.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY,
		name TEXT,
		outbound_format_override TEXT NOT NULL DEFAULT '',
		upstream_protocols TEXT NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatalf("create channels table: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO channels (id, name, outbound_format_override, upstream_protocols) VALUES (1, 'explicit', 'chat_only', ?)",
		`["responses","chat"]`,
	).Error; err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	if err := addChannelUpstreamProtocols(db); err != nil {
		t.Fatalf("addChannelUpstreamProtocols: %v", err)
	}

	var stored string
	if err := db.Table("channels").Select("upstream_protocols").Where("id = 1").Scan(&stored).Error; err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if stored != `["responses","chat"]` {
		t.Fatalf("upstream_protocols = %q, want the explicit declaration to be preserved", stored)
	}
}
