package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/setting"
)

func configureModelCatalogRouteCandidates(t *testing.T, channelID int, candidates []string) {
	t.Helper()

	fixtureGroup, ok := group.GetCache().Get(channelID)
	if !ok {
		t.Fatalf("fixture group %d is missing from cache", channelID)
	}
	fixtureGroup.Mode = dbmodel.GroupModeFailover
	fixtureGroup.RelayRetryCount = -1
	fixtureGroup.RelayRouteRetries = -1
	fixtureGroup.Items = make([]dbmodel.GroupItem, 0, len(candidates))
	for index, upstreamModel := range candidates {
		fixtureGroup.Items = append(fixtureGroup.Items, dbmodel.GroupItem{
			ChannelID: channelID,
			ModelName: upstreamModel,
			Priority:  index + 1,
			Weight:    1,
		})
	}
	group.GetCache().Set(channelID, fixtureGroup)
	group.RebuildIndexes()
	if err := setting.SetString(dbmodel.SettingKeyRelayMaxTotalAttempts, fmt.Sprint(len(candidates))); err != nil {
		t.Fatalf("set relay max total attempts: %v", err)
	}
}

func modelCatalogErrorResponse(code string) string {
	return fmt.Sprintf(`{"error":{"code":%q,"message":"fixture rejected model","type":"invalid_request_error"}}`, code)
}

func TestHandlerAllConfiguredModelTargetsRejectedReturnsSpecific503(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var upstreamCalls atomic.Int32
	fixtureID := prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		upstreamCalls.Add(1)
		if body.Model != "upstream-model-a" && body.Model != "upstream-model-b" {
			return nil, fmt.Errorf("unexpected upstream model %q", body.Model)
		}
		return newIndependentFixtureResponse(request, http.StatusNotFound, modelCatalogErrorResponse("model_not_found")), nil
	})
	configureModelCatalogRouteCandidates(t, fixtureID, []string{"upstream-model-a", "upstream-model-b"})

	recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"fixture"}]}`)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", recorder.Code, recorder.Body.String())
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream attempts = %d, want both configured model mappings tried", upstreamCalls.Load())
	}
	for _, expected := range []string{
		`"type":"upstream_unavailable"`,
		`"code":"upstream_model_unavailable"`,
		"当前没有上游接受分组配置的模型映射",
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("response missing %q: %s", expected, recorder.Body.String())
		}
	}
}

func TestHandlerMixedModelTargetFailuresKeepGenericUnavailableResponse(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var upstreamCalls atomic.Int32
	fixtureID := prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		upstreamCalls.Add(1)
		if body.Model == "upstream-model-a" {
			return newIndependentFixtureResponse(request, http.StatusNotFound, modelCatalogErrorResponse("model_not_found")), nil
		}
		if body.Model != "upstream-model-b" {
			return nil, fmt.Errorf("unexpected upstream model %q", body.Model)
		}
		return newIndependentFixtureResponse(request, http.StatusInternalServerError, `{"error":{"type":"api_error","message":"fixture unavailable"}}`), nil
	})
	configureModelCatalogRouteCandidates(t, fixtureID, []string{"upstream-model-a", "upstream-model-b"})

	recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"fixture"}]}`)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want generic 502 for mixed failures: %s", recorder.Code, recorder.Body.String())
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream attempts = %d, want both configured model mappings tried", upstreamCalls.Load())
	}
	if strings.Contains(recorder.Body.String(), "upstream_model_unavailable") {
		t.Fatalf("mixed upstream failures were incorrectly reported as model-only failure: %s", recorder.Body.String())
	}
}
