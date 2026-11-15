package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestDraftGroupProbeEvidenceIsFrozenBeforeLogPersistence(t *testing.T) {
	setupHelperDB(t)
	t.Cleanup(relaylog.SetCacheForTest(nil))
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"content":"safe"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	ch := m.Channel{ID: 70, Enabled: true, Name: "saved-channel", Type: outbound.OutboundTypeOpenAIChat,
		BaseUrls: []m.BaseUrl{{URL: server.URL}}, Keys: []m.ChannelKey{{ID: 71, ChannelID: 70, Enabled: true, ChannelKey: "synthetic-only"}}}
	gr := &m.Group{EndpointType: m.EndpointTypeChat, Items: []m.GroupItem{{ID: 72, ChannelID: 70, ModelName: "full-prefixed-model"}}}
	gr.RequestTimeoutSeconds = 1
	progress := &GroupModelTestProgress{ID: "frozen-draft", Source: "draft", SnapshotHash: "frozen-snapshot"}
	summary, err := runGroupModelTest(context.Background(), gr, map[int]m.Channel{70: ch}, progress)
	if err != nil || !summary.Passed {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	logs, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	if len(logs) != 1 {
		lock.Unlock()
		t.Fatalf("logs=%d", len(logs))
	}
	entry := logs[0]
	lock.Unlock()
	if entry.Diagnostics == nil || entry.Diagnostics.Probe == nil || entry.Diagnostics.Probe.Source != "draft" || entry.Diagnostics.Probe.ConfigurationFingerprint != "frozen-snapshot" || entry.Diagnostics.Probe.KeyID != 71 {
		t.Fatalf("wrong persisted draft evidence: %+v", entry.Diagnostics)
	}
	if budget := entry.Diagnostics.Probe.RequestBudgetMS; budget == nil || *budget <= 0 || *budget > int64(time.Second/time.Millisecond) {
		t.Fatalf("actual draft budget was not recorded: %v", budget)
	}
	if err := relaylog.RelayLogSaveDBTask(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored m.RelayLog
	if err := db.GetLogDB().First(&stored, entry.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Diagnostics.Probe.Source != "draft" || stored.Diagnostics.Probe.ConfigurationFingerprint != "frozen-snapshot" {
		t.Fatal("DB lost draft provenance")
	}
}
