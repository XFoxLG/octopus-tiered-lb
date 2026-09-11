package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestRequestFilterChecksOnlyLatestUserText(testContext *testing.T) {
	testCases := []struct {
		name        string
		requestJSON string
		keywords    []string
		wantBlocked bool
	}{
		{"case insensitive phrase", `{"messages":[{"role":"user","content":"Please run HEALTH PROBE now"}]}`, []string{"health probe"}, true},
		{"Chinese phrase", `{"messages":[{"role":"user","content":"\u6d4b\u6d3b"}]}`, []string{"\u6d4b\u6d3b"}, true},
		{"split text parts", `{"messages":[{"role":"user","content":[{"type":"text","text":"health "},{"type":"text","text":"probe"}]}]}`, []string{"health probe"}, true},
		{"old user turn ignored", `{"messages":[{"role":"user","content":"health probe"},{"role":"assistant","content":"health probe"},{"role":"user","content":"Continue the story"}]}`, []string{"health probe"}, false},
		{"system and assistant ignored", `{"messages":[{"role":"system","content":"health probe"},{"role":"user","content":"Continue the story"},{"role":"assistant","content":"health probe"}]}`, []string{"health probe"}, false},
		{"tool results ignored", `{"messages":[{"role":"user","content":"Continue the story"},{"role":"tool","content":"health probe"}]}`, []string{"health probe"}, false},
		{"image URL ignored", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/health-probe.png"}}]}]}`, []string{"health-probe"}, false},
		{"blank keywords ignored", `{"messages":[{"role":"user","content":"ordinary words"}]}`, []string{"", "  "}, false},
		{"no conversation input", `{"model":"embedding-fixture"}`, []string{"health probe"}, false},
	}
	for _, testCase := range testCases {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			var request model.InternalLLMRequest
			if err := jsonAPI.Unmarshal([]byte(testCase.requestJSON), &request); err != nil {
				testContext.Fatal(err)
			}
			beforeFiltering, _ := jsonAPI.Marshal(&request)
			filterConfig := requestFilterConfig{Enabled: true, Keywords: testCase.keywords}
			if blocked := shouldBlockRequest(&request, filterConfig); blocked != testCase.wantBlocked {
				testContext.Fatalf("blocked = %t, want %t", blocked, testCase.wantBlocked)
			}
			afterFiltering, _ := jsonAPI.Marshal(&request)
			if string(beforeFiltering) != string(afterFiltering) {
				testContext.Fatal("input filtering must not rewrite the request")
			}
			filterConfig.Enabled = false
			if shouldBlockRequest(&request, filterConfig) {
				testContext.Fatal("disabled input filtering blocked a request")
			}
		})
	}
}

func TestHandlerRequestFilterRejectsBeforeAnyUpstreamAttempt(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	var upstreamCalls atomic.Int64
	prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return newIndependentFixtureResponse(request, http.StatusBadGateway, `{"error":{"message":"must not reach upstream"}}`), nil
	})
	for settingKey, value := range map[dbmodel.SettingKey]string{
		dbmodel.SettingKeyRequestFilterEnabled:      "true",
		dbmodel.SettingKeyRequestFilterKeywords:     `["health probe"]`,
		dbmodel.SettingKeyRequestFilterErrorMessage: "Local policy: no probing",
		dbmodel.SettingKeyRelayMaxTotalAttempts:     "8",
	} {
		if err := setting.SetString(settingKey, value); err != nil {
			testContext.Fatal(err)
		}
	}
	protocols := []struct {
		name        string
		path        string
		inboundType inbound.InboundType
		bodyFormat  string
	}{
		{"chat", "/v1/chat/completions", inbound.InboundTypeOpenAIChat, `{"model":"creative-fixture","messages":[{"role":"user","content":"HEALTH PROBE"}],"stream":%t}`},
		{"responses", "/v1/responses", inbound.InboundTypeOpenAIResponse, `{"model":"creative-fixture","input":"HEALTH PROBE","stream":%t}`},
		{"anthropic", "/v1/messages", inbound.InboundTypeAnthropic, `{"model":"creative-fixture","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"HEALTH PROBE"}]}],"stream":%t}`},
	}
	for _, protocol := range protocols {
		for _, streaming := range []bool{false, true} {
			testContext.Run(fmt.Sprintf("%s/stream=%t", protocol.name, streaming), func(testContext *testing.T) {
				recorder := httptest.NewRecorder()
				requestContext, _ := gin.CreateTestContext(recorder)
				requestContext.Request = httptest.NewRequest(http.MethodPost, protocol.path, strings.NewReader(fmt.Sprintf(protocol.bodyFormat, streaming)))
				requestContext.Request.Header.Set("Content-Type", "application/json")
				requestContext.Set("api_key_id", 890000)
				Handler(dbmodel.EndpointTypeChat, protocol.inboundType, requestContext)
				if recorder.Code != http.StatusForbidden {
					testContext.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
				}
				var payload struct {
					Error struct {
						Message string `json:"message"`
						Code    string `json:"code"`
					} `json:"error"`
				}
				if err := jsonAPI.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					testContext.Fatal(err)
				}
				if payload.Error.Code != "input_keyword_blocked" || payload.Error.Message != "Local policy: no probing" {
					testContext.Fatalf("unexpected rejection: %s", recorder.Body.String())
				}
				if upstreamCalls.Load() != 0 {
					testContext.Fatalf("blocked request made %d upstream calls", upstreamCalls.Load())
				}
			})
		}
	}
}

func TestHandlerInputFilterDoesNotFilterModelOutputOrReuseLegacyRules(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	var upstreamCalls atomic.Int64
	prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Stream bool `json:"stream"`
		}
		if err := jsonAPI.Unmarshal(requestBody, &payload); err != nil {
			return nil, err
		}
		if payload.Stream {
			response := newIndependentFixtureResponse(request, http.StatusOK,
				"data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"creative-fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"health probe in a story\"},\"finish_reason\":null}]}\n\n"+
					"data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"creative-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			response.Header.Set("Content-Type", "text/event-stream")
			return response, nil
		}
		return newIndependentFixtureResponse(request, http.StatusOK, `{"id":"fixture","object":"chat.completion","model":"creative-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"health probe in a story"},"finish_reason":"stop"}]}`), nil
	})
	// Stored output-filter settings survive upgrades but no longer affect input
	// or output. In particular, legacy enabled=true must not activate the new rule.
	for settingKey, value := range map[dbmodel.SettingKey]string{
		"response_filter_enabled": "true", "response_filter_keywords": `["health probe"]`, "response_filter_action": "replace",
	} {
		setting.GetCache().Set(settingKey, value)
	}
	if loadRequestFilterConfig().Enabled {
		testContext.Fatal("legacy output settings enabled input filtering")
	}
	for _, streaming := range []bool{false, true} {
		requestBody := fmt.Sprintf(`{"model":"creative-fixture","messages":[{"role":"user","content":"health probe"}],"stream":%t}`, streaming)
		recorder := serveIndependentFixtureRequest(requestBody)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "health probe in a story") {
			testContext.Fatalf("legacy settings changed the response: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if err := setting.SetString(dbmodel.SettingKeyRequestFilterEnabled, "true"); err != nil {
		testContext.Fatal(err)
	}
	if err := setting.SetString(dbmodel.SettingKeyRequestFilterKeywords, `["health probe"]`); err != nil {
		testContext.Fatal(err)
	}
	for _, streaming := range []bool{false, true} {
		requestBody := fmt.Sprintf(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}],"stream":%t}`, streaming)
		recorder := serveIndependentFixtureRequest(requestBody)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "health probe in a story") {
			testContext.Fatalf("input filtering changed model output: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if upstreamCalls.Load() != 4 {
		testContext.Fatalf("upstream calls = %d, want exactly 4", upstreamCalls.Load())
	}
}
