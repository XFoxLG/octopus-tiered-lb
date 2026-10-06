package channel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
)

func TestReviewApplyProtocolsSerializationAndCache(t *testing.T) {
	for _, tc := range []struct {
		name      string
		protocols []string
	}{
		{"single", []string{"chat_only"}},
		{"multiple", []string{"chat_only", "responses_only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupBatchGroupTest(t)
			seedChannel(t, 981, 1)
			added, err := ApplyProbeProtocols(context.Background(), 981, tc.protocols)
			t.Logf("added=%v error=%v", added, err)
			if err != nil {
				t.Errorf("applying valid protocols failed: %v", err)
			}
			var stored string
			if err := db.GetDB().Table("channels").Select("upstream_protocols").Where("id = ?", 981).Scan(&stored).Error; err != nil {
				t.Fatal(err)
			}
			t.Logf("raw DB value=%q", stored)
			if !json.Valid([]byte(stored)) {
				t.Errorf("protocol value is not valid JSON: %q", stored)
			}
			if _, err := Get(981, context.Background()); err != nil {
				t.Errorf("channel disappeared from runtime cache after apply: %v", err)
			}
		})
	}
}
