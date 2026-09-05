package notification

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestSetMessage_WithArgs(t *testing.T) {
	n := &model.Notification{}
	SetMessage(n, KeyBackupOK, KeyBackupOK,
		map[string]any{"file": "octopus-2026.db"},
		map[string]any{"file": "octopus-2026.db", "size": 1024},
		nil,
		[]any{"octopus-2026.db", 1024})

	// i18n 键
	if n.TitleKey != "backup.ok" {
		t.Fatalf("expected title_key 'backup.ok', got %q", n.TitleKey)
	}
	if n.ContentKey != "backup.ok" {
		t.Fatalf("expected content_key 'backup.ok', got %q", n.ContentKey)
	}

	// 参数 JSON 可反序列化
	var titleArgs map[string]any
	if err := json.Unmarshal([]byte(n.TitleArgs), &titleArgs); err != nil {
		t.Fatalf("failed to parse title_args: %v", err)
	}
	if titleArgs["file"] != "octopus-2026.db" {
		t.Fatalf("expected title_args.file='octopus-2026.db', got %v", titleArgs["file"])
	}

	var contentArgs map[string]any
	if err := json.Unmarshal([]byte(n.ContentArgs), &contentArgs); err != nil {
		t.Fatalf("failed to parse content_args: %v", err)
	}
	if contentArgs["size"].(float64) != 1024 {
		t.Fatalf("expected content_args.size=1024, got %v", contentArgs["size"])
	}

	// 英文回退串
	if !strings.Contains(n.Title, "octopus-2026.db") {
		t.Fatalf("expected title fallback to contain file name, got %q", n.Title)
	}
	if !strings.Contains(n.Content, "1024") {
		t.Fatalf("expected content fallback to contain size, got %q", n.Content)
	}
}

func TestSetMessage_NoArgs(t *testing.T) {
	// 无参数的键（如 backup.skip）应留空 *Args，回退串为模板原文。
	n := &model.Notification{}
	SetMessage(n, KeyBackupSkip, KeyBackupSkip, nil, nil, nil, nil)

	if n.TitleKey != "backup.skip" {
		t.Fatalf("expected title_key 'backup.skip', got %q", n.TitleKey)
	}
	if n.TitleArgs != "" {
		t.Fatalf("expected empty title_args for nil args, got %q", n.TitleArgs)
	}
	if n.ContentArgs != "" {
		t.Fatalf("expected empty content_args for nil args, got %q", n.ContentArgs)
	}
	// 回退串应为模板原文（无 Sprintf）
	if n.Title == "" {
		t.Fatal("expected non-empty fallback title")
	}
	if n.Content == "" {
		t.Fatal("expected non-empty fallback content")
	}
}

func TestSetMessage_NumericArgs(t *testing.T) {
	// 整型参数应正确序列化进 JSON。
	n := &model.Notification{}
	SetMessage(n, KeyKeyHealthFail, KeyKeyHealthFail,
		map[string]any{"name": "ch-1"},
		map[string]any{"name": "ch-1", "id": 7, "fails": 3, "detail": "timeout"},
		[]any{"ch-1"},
		[]any{"ch-1", 7, 3, "timeout"})

	var contentArgs map[string]any
	if err := json.Unmarshal([]byte(n.ContentArgs), &contentArgs); err != nil {
		t.Fatalf("failed to parse content_args: %v", err)
	}
	// JSON 数字反序列化为 float64
	if contentArgs["fails"].(float64) != 3 {
		t.Fatalf("expected fails=3, got %v", contentArgs["fails"])
	}
	if contentArgs["id"].(float64) != 7 {
		t.Fatalf("expected id=7, got %v", contentArgs["id"])
	}
	if !strings.Contains(n.Content, "timeout") {
		t.Fatalf("expected fallback content to contain detail, got %q", n.Content)
	}
}

func TestAllKeysHaveFallbackTemplates(t *testing.T) {
	// 确保每个 NotifKey 常量都有对应的英文回退 title/content 模板，避免漏配。
	allKeys := []NotifKey{
		KeyChannelExpire,
		KeySiteBatch, KeySiteAccountOK, KeySiteAccountFail,
		KeyBackupOK, KeyBackupFail, KeyBackupSkip,
		KeyRestoreOK, KeyRestoreFail,
		KeyMigrationOK, KeyMigrationFail,
		KeySelfUpdateOK, KeySelfUpdateFail,
		KeyKeyHealthFail, KeyKeyHealthRecover,
	}
	for _, k := range allKeys {
		if _, ok := fallbackTitle[k]; !ok {
			t.Errorf("missing fallback title for key %q", k)
		}
		if _, ok := fallbackContent[k]; !ok {
			t.Errorf("missing fallback content for key %q", k)
		}
	}
}
