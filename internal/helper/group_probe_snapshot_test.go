package helper

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
)

func TestSavedGroupSnapshotHashFreezesRouteSettings(t *testing.T) {
	group := &appmodel.Group{
		ID:                     9,
		Name:                   "saved",
		DefaultReasoningEffort: "high",
		RequestTimeoutSeconds:  900,
		StreamKeepaliveEnabled: true,
		ReasoningForceOverride: true,
		RelayRetryCount:        2,
		Items:                  []appmodel.GroupItem{{ChannelID: 4, ModelName: "model"}},
	}
	channels := map[int]appmodel.Channel{
		4: {
			ID: 4, Name: "channel", Type: 1,
			Keys: []appmodel.ChannelKey{{ID: 8, Enabled: true, ChannelKey: "secret"}},
		},
	}
	first := savedGroupSnapshotHash(group, channels)
	second := savedGroupSnapshotHash(group, channels)
	if first == "" || first != second || len(first) != 64 {
		t.Fatalf("saved snapshot hash is not stable: %q %q", first, second)
	}

	group.DefaultReasoningEffort = "low"
	if changed := savedGroupSnapshotHash(group, channels); changed == first {
		t.Fatal("changing saved thinking config did not change the snapshot hash")
	}
}
