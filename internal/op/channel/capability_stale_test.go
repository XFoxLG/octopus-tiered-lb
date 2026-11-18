package channel

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/crypto"
)

func setupCapabilityTest(t *testing.T) {
	t.Helper()
	setupBatchGroupTest(t)
	crypto.Init("synthetic-capability-test-key")
	if err := db.GetDB().AutoMigrate(&model.ChannelModelCapability{}); err != nil {
		t.Fatalf("migrate capabilities: %v", err)
	}
	chCache.Clear()
	keyCache.Clear()
}

func seedScopedCapability(t *testing.T, channelID int, endpointID, protocol string, keyID int) model.ChannelModelCapability {
	t.Helper()
	ch, err := Get(channelID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record := model.ChannelModelCapability{
		ChannelID:  channelID,
		ChannelUID: ch.ChannelUID,
		EndpointID: endpointID,
		Protocol:   protocol,
		ModelName:  "gemini-3.8-flash",
		Capability: model.CapabilityToolCalling,
		Supported:  true,
		Source:     "probe",
		ProbeKeyID: keyID,
	}
	if err := UpsertCapability(context.Background(), record); err != nil {
		t.Fatalf("upsert capability: %v", err)
	}
	var stored model.ChannelModelCapability
	if err := db.GetDB().Where(
		"channel_id = ? AND endpoint_id = ? AND protocol = ? AND probe_key_id = ?",
		channelID, endpointID, protocol, keyID,
	).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestCapabilityScopesDoNotOverwriteEachOther(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)

	seedScopedCapability(t, 1, "chat", "chat", 10)
	seedScopedCapability(t, 1, "messages", "messages", 10)
	seedScopedCapability(t, 1, "chat", "chat", 11)
	legacy := seedScopedCapability(t, 1, "", "", 10)
	if legacy.ChannelUID == "" {
		t.Fatal("legacy evidence has no stable channel scope")
	}

	current, err := ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 4 {
		t.Fatalf("current capability rows = %d, want 4 independent scopes", len(current))
	}
}

func TestCapabilityEvidenceBecomesStaleOnlyForTransportChanges(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)
	seedScopedCapability(t, 1, "", "", 10)

	name := "renamed"
	timeout := 120
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, Name: &name, AttemptTimeOut: &timeout}, context.Background()); err != nil {
		t.Fatalf("non-transport edit failed: %v", err)
	}
	if rows, err := ListCapabilities(context.Background(), 1, "", false); err != nil || len(rows) != 1 {
		t.Fatalf("rename invalidated evidence: rows=%d err=%v", len(rows), err)
	}

	baseURLs := []model.BaseUrl{{URL: "https://changed.example/v1"}}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, BaseUrls: &baseURLs}, context.Background()); err != nil {
		t.Fatalf("transport edit failed: %v", err)
	}
	rows, err := ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("stale row was returned as current: %+v", rows)
	}
	all, err := ListCapabilities(context.Background(), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !all[0].Stale || all[0].StaleReason == "" {
		t.Fatalf("history was not retained as stale: %+v", all)
	}
}

func TestCapabilityUnchangedResubmittedFieldsKeepEvidenceCurrent(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)
	seedScopedCapability(t, 1, "", "", 10)

	override := `{"temperature":0.7}`
	headers := []model.CustomHeader{{HeaderKey: "X-Synthetic", HeaderValue: "unchanged"}}
	rewrite := &model.RequestRewriteConfig{Enabled: false}
	if _, err := Update(&model.ChannelUpdateRequest{
		ID: 1, ParamOverride: &override, CustomHeader: &headers, RequestRewrite: rewrite,
	}, context.Background()); err != nil {
		t.Fatalf("seed editable transport settings: %v", err)
	}
	seedScopedCapability(t, 1, "", "", 10)

	if _, err := Update(&model.ChannelUpdateRequest{
		ID: 1, ParamOverride: &override, CustomHeader: &headers, RequestRewrite: rewrite,
	}, context.Background()); err != nil {
		t.Fatalf("resubmit unchanged settings: %v", err)
	}
	rows, err := ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("unchanged fields invalidated evidence: rows=%d", len(rows))
	}

	changed := `{"temperature":0.9}`
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ParamOverride: &changed}, context.Background()); err != nil {
		t.Fatalf("change parameter override: %v", err)
	}
	rows, err = ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("changed parameter override kept evidence current: %+v", rows)
	}
}

func TestCapabilityKeyCredentialChangeMarksOnlyThatKeyStale(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)
	keys := []model.ChannelKey{
		{ID: 21, ChannelID: 1, Enabled: true, ChannelKey: "first"},
		{ID: 22, ChannelID: 1, Enabled: true, ChannelKey: "second"},
	}
	if err := db.GetDB().Create(&keys).Error; err != nil {
		t.Fatal(err)
	}
	if err := RefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedScopedCapability(t, 1, "chat", "chat", 21)
	seedScopedCapability(t, 1, "chat", "chat", 22)

	replacement := "rotated"
	if _, err := Update(&model.ChannelUpdateRequest{
		ID:           1,
		KeysToUpdate: []model.ChannelKeyUpdateRequest{{ID: 21, ChannelKey: &replacement}},
	}, context.Background()); err != nil {
		t.Fatalf("key update failed: %v", err)
	}

	rows, err := ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ProbeKeyID != 22 {
		t.Fatalf("key-scoped invalidation leaked: %+v", rows)
	}
	all, err := ListCapabilities(context.Background(), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("history row count = %d, want 2", len(all))
	}
	for _, row := range all {
		if row.ProbeKeyID == 21 && (!row.Stale || row.StaleReason != "configuration_changed") {
			t.Fatalf("rotated key evidence is still current: %+v", row)
		}
	}
}

func TestCapabilityEndpointChangeMarksOnlyThatEndpointStale(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)

	chat := model.ChannelEndpoint{
		ID:       "chat",
		Protocol: "chat",
		URL:      "https://upstream.example/v1/chat/completions",
		URLMode:  "base",
		Auth:     "bearer",
	}
	messages := model.ChannelEndpoint{
		ID:       "messages",
		Protocol: "messages",
		URL:      "https://upstream.example/v1/messages",
		URLMode:  "base",
		Auth:     "x_api_key",
	}
	config := &model.ConnectionConfig{
		Version:   model.ConnectionConfigVersionDirect,
		Selection: "configured",
		Endpoints: []model.ChannelEndpoint{chat, messages},
		Catalog:   model.ModelCatalog{Format: "manual"},
	}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: config}, context.Background()); err != nil {
		t.Fatalf("add configured connection: %v", err)
	}

	seedScopedCapability(t, 1, chat.ID, chat.Protocol, 10)
	seedScopedCapability(t, 1, messages.ID, messages.Protocol, 10)

	changedChat := chat
	changedChat.URL = "https://changed.example/v1/chat/completions"
	nextConfig := &model.ConnectionConfig{
		Version:   model.ConnectionConfigVersionDirect,
		Selection: "configured",
		Endpoints: []model.ChannelEndpoint{changedChat, messages},
		Catalog:   model.ModelCatalog{Format: "manual"},
	}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: nextConfig}, context.Background()); err != nil {
		t.Fatalf("update one endpoint: %v", err)
	}

	rows, err := ListCapabilities(context.Background(), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("history row count = %d, want 2", len(rows))
	}
	for _, row := range rows {
		switch row.EndpointID {
		case chat.ID:
			if !row.Stale {
				t.Fatalf("changed endpoint remains current: %+v", row)
			}
		case messages.ID:
			if row.Stale {
				t.Fatalf("unchanged endpoint was invalidated: %+v", row)
			}
		default:
			t.Fatalf("unexpected endpoint scope: %+v", row)
		}
	}
}

func TestCapabilityDeletedEndpointIsStale(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)

	chat := model.ChannelEndpoint{ID: "chat", Protocol: "chat", URL: "https://upstream.example/chat", URLMode: "base", Auth: "bearer"}
	messages := model.ChannelEndpoint{ID: "messages", Protocol: "messages", URL: "https://upstream.example/messages", URLMode: "base", Auth: "x_api_key"}
	config := &model.ConnectionConfig{
		Version:   model.ConnectionConfigVersionDirect,
		Selection: "configured",
		Endpoints: []model.ChannelEndpoint{chat, messages},
		Catalog:   model.ModelCatalog{Format: "manual"},
	}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: config}, context.Background()); err != nil {
		t.Fatalf("add configured connection: %v", err)
	}
	seedScopedCapability(t, 1, chat.ID, chat.Protocol, 10)
	seedScopedCapability(t, 1, messages.ID, messages.Protocol, 10)

	nextConfig := &model.ConnectionConfig{
		Version:   model.ConnectionConfigVersionDirect,
		Selection: "configured",
		Endpoints: []model.ChannelEndpoint{chat},
		Catalog:   model.ModelCatalog{Format: "manual"},
	}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: nextConfig}, context.Background()); err != nil {
		t.Fatalf("delete one endpoint: %v", err)
	}

	rows, err := ListCapabilities(context.Background(), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("history row count = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.EndpointID == messages.ID && !row.Stale {
			t.Fatalf("deleted endpoint evidence remains current: %+v", row)
		}
		if row.EndpointID == chat.ID && row.Stale {
			t.Fatalf("surviving endpoint evidence was invalidated: %+v", row)
		}
	}
}

func TestCapabilityRenameOnConfiguredChannelKeepsEndpointEvidenceCurrent(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)

	config := &model.ConnectionConfig{
		Version:   model.ConnectionConfigVersionDirect,
		Selection: "configured",
		Endpoints: []model.ChannelEndpoint{{
			ID: "chat", Protocol: "chat", URL: "https://upstream.example/chat", URLMode: "base", Auth: "bearer",
		}},
		Catalog: model.ModelCatalog{Format: "manual"},
	}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: config}, context.Background()); err != nil {
		t.Fatalf("add configured connection: %v", err)
	}
	seedScopedCapability(t, 1, "chat", "chat", 10)

	name := "renamed"
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, Name: &name}, context.Background()); err != nil {
		t.Fatalf("rename configured channel: %v", err)
	}
	rows, err := ListCapabilities(context.Background(), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Stale || rows[0].EndpointID != "chat" {
		t.Fatalf("display-only rename invalidated endpoint evidence: %+v", rows)
	}
}

func TestCapabilityProbeIsRejectedForDraftChannelOnly(t *testing.T) {
	setupCapabilityTest(t)
	seedChannel(t, 1, 1)
	if err := UpsertCapability(context.Background(), model.ChannelModelCapability{
		ChannelID: 1, ModelName: "m", Capability: model.CapabilityStreaming, Supported: true, Source: "probe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertCapability(context.Background(), model.ChannelModelCapability{
		ChannelID: 0, ModelName: "m", Capability: model.CapabilityStreaming, Supported: true,
	}); err != nil {
		t.Fatalf("draft channel capability should be a no-op: %v", err)
	}
}

func TestCapabilityFingerprintIgnoresDisplayFieldsAndTracksTransport(t *testing.T) {
	channel := &model.Channel{
		ID:       1,
		Name:     "first",
		Type:     outbound.OutboundTypeOpenAIChat,
		GroupID:  3,
		BaseUrls: []model.BaseUrl{{URL: "https://one.example/v1"}},
	}
	original := model.CapabilityConfigurationFingerprint(channel, "", "chat")
	renamed := *channel
	renamed.Name = "second"
	renamed.GroupID = 9
	renamed.Model = "different-display-model"
	if got := model.CapabilityConfigurationFingerprint(&renamed, "", "chat"); got != original {
		t.Fatalf("display-only fields changed fingerprint: %s != %s", got, original)
	}
	moved := *channel
	moved.BaseUrls = []model.BaseUrl{{URL: "https://two.example/v1"}}
	if got := model.CapabilityConfigurationFingerprint(&moved, "", "chat"); got == original {
		t.Fatal("transport URL change did not change fingerprint")
	}
	// The periodic delay probe rewrites only Delay. Changing it must not make
	// observed capability evidence look stale.
	measured := *channel
	measured.BaseUrls = []model.BaseUrl{{URL: "https://one.example/v1", Delay: 42}}
	if got := model.CapabilityConfigurationFingerprint(&measured, "", "chat"); got != original {
		t.Fatalf("delay measurement changed fingerprint: %s != %s", got, original)
	}
	protocolAddressed := *channel
	protocolAddressed.BaseUrls = []model.BaseUrl{{URL: "https://one.example/v1", Protocol: "messages"}}
	if got := model.CapabilityConfigurationFingerprint(&protocolAddressed, "", "chat"); got == original {
		t.Fatal("protocol selection on a base URL did not change fingerprint")
	}
	if model.CapabilityConfigurationFingerprint(nil, "", "chat") != "" {
		t.Fatal("nil channel produced a fingerprint")
	}
}
