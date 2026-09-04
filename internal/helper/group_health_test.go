package helper

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
)

func healthTestItems(channelIDs ...int) []appmodel.GroupItem {
	items := make([]appmodel.GroupItem, 0, len(channelIDs))
	for i, id := range channelIDs {
		items = append(items, appmodel.GroupItem{ID: i + 1, ChannelID: id, ModelName: "m"})
	}
	return items
}

func TestResolveGroupHealthFinalStatus(t *testing.T) {
	items := healthTestItems(7, 9)

	status, _ := resolveGroupHealthFinalStatus(appmodel.GroupModeFailover, false, nil, false, -1, 0, 0)
	if status != appmodel.GroupHealthStatusFailed {
		t.Fatalf("empty items status = %s, want failed", status)
	}
	status, _ = resolveGroupHealthFinalStatus(appmodel.GroupModeRoundRobin, false, items, false, -1, 2, 0)
	if status != appmodel.GroupHealthStatusFailed {
		t.Fatalf("all failed status = %s, want failed", status)
	}
	status, _ = resolveGroupHealthFinalStatus(appmodel.GroupModeFailover, true, items, true, 0, 1, 1)
	if status != appmodel.GroupHealthStatusSuccess {
		t.Fatalf("failover first success status = %s, want success", status)
	}
	status, _ = resolveGroupHealthFinalStatus(appmodel.GroupModeFailover, true, items, true, 1, 2, 1)
	if status != appmodel.GroupHealthStatusPartial {
		t.Fatalf("failover second success status = %s, want partial", status)
	}
	status, _ = resolveGroupHealthFinalStatus(appmodel.GroupModeRoundRobin, false, items, true, 0, 2, 2)
	if status != appmodel.GroupHealthStatusSuccess {
		t.Fatalf("all success status = %s, want success", status)
	}
	status, _ = resolveGroupHealthFinalStatus(appmodel.GroupModeRoundRobin, false, items, true, 0, 2, 1)
	if status != appmodel.GroupHealthStatusPartial {
		t.Fatalf("partial success status = %s, want partial", status)
	}
}

func TestResolveGroupHealthProbeMode(t *testing.T) {
	if got := resolveGroupHealthProbeMode(nil); got != appmodel.GroupHealthProbeModeStandard {
		t.Fatalf("empty modes = %s, want standard", got)
	}
	if got := resolveGroupHealthProbeMode([]appmodel.GroupHealthProbeMode{appmodel.GroupHealthProbeModeFull}); got != appmodel.GroupHealthProbeModeFull {
		t.Fatalf("first full = %s, want full", got)
	}
	if got := resolveGroupHealthProbeMode([]appmodel.GroupHealthProbeMode{appmodel.GroupHealthProbeModeStandard, appmodel.GroupHealthProbeModeFull}); got != appmodel.GroupHealthProbeModeStandard {
		t.Fatalf("full not first = %s, want standard", got)
	}
}
