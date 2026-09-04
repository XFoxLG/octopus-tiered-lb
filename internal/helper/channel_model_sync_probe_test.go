package helper

import (
	"context"
	"strings"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func syncProbeTestChannel() *appmodel.Channel {
	return &appmodel.Channel{
		Name:        "sync-probe",
		Type:        outbound.OutboundTypeOpenAIChat,
		BaseUrls:    []appmodel.BaseUrl{{URL: "https://example.com"}},
		Model:       "gpt-4o",
		CustomModel: "custom-model",
		Keys:        []appmodel.ChannelKey{{Enabled: true, ChannelKey: "sk-test"}},
	}
}

func TestTestChannelModelSyncRejectsBadParams(t *testing.T) {
	ctx := context.Background()
	valid := syncProbeTestChannel()

	if _, err := TestChannelModelSync(ctx, nil, "gpt-4o", 0, "", 0); err == nil {
		t.Fatal("nil channel: want error, got nil")
	}
	if _, err := TestChannelModelSync(ctx, valid, "  ", 0, "", 0); err == nil {
		t.Fatal("empty model: want error, got nil")
	}
	if _, err := TestChannelModelSync(ctx, valid, "gpt-4o", 3, "", 0); err == nil {
		t.Fatal("key_index out of range: want error, got nil")
	}
	emptyKey := syncProbeTestChannel()
	emptyKey.Keys[0].ChannelKey = "  "
	if _, err := TestChannelModelSync(ctx, emptyKey, "gpt-4o", 0, "", 0); err == nil {
		t.Fatal("empty key: want error, got nil")
	}
	skipped := syncProbeTestChannel()
	skipped.SkipModelTest = true
	if _, err := TestChannelModelSync(ctx, skipped, "gpt-4o", 0, "", 0); err == nil {
		t.Fatal("SkipModelTest channel: want error, got nil")
	}
	unknown := syncProbeTestChannel()
	unknown.Type = outbound.OutboundType(9999)
	if _, err := TestChannelModelSync(ctx, unknown, "gpt-4o", 0, "", 0); err == nil {
		t.Fatal("unsupported channel type: want error, got nil")
	}
}

func TestChannelModelInSelectedModels(t *testing.T) {
	channel := syncProbeTestChannel()
	if !ChannelModelInSelectedModels(channel, "gpt-4o") {
		t.Fatal("model in Model list: want true, got false")
	}
	if !ChannelModelInSelectedModels(channel, "custom-model") {
		t.Fatal("model in CustomModel list: want true, got false")
	}
	if ChannelModelInSelectedModels(channel, "other-model") {
		t.Fatal("model not selected: want false, got true")
	}
	if ChannelModelInSelectedModels(nil, "gpt-4o") {
		t.Fatal("nil channel: want false, got true")
	}
}

func TestTruncateSyncProbeErr(t *testing.T) {
	if got := truncateSyncProbeErr("short"); got != "short" {
		t.Fatalf("short error = %q, want unchanged", got)
	}
	long := strings.Repeat("x", 300)
	got := truncateSyncProbeErr(long)
	if len(got) != 203 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long error len = %d, want 203 with ... suffix", len(got))
	}
}
