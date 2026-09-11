package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/client"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

func TestHandlerRetryStartsWithFreshProtocolState(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	protocols := []struct {
		name        string
		inboundType inbound.InboundType
		requestPath string
		requestBody string
		startEvent  string
	}{
		{"chat", inbound.InboundTypeOpenAIChat, "/v1/chat/completions", `{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}],"stream":true}`, `"id":"successful-attempt"`},
		{"responses", inbound.InboundTypeOpenAIResponse, "/v1/responses", `{"model":"creative-fixture","input":"Continue the story","stream":true}`, `"type":"response.created"`},
		{"anthropic", inbound.InboundTypeAnthropic, "/v1/messages", `{"model":"creative-fixture","max_tokens":128,"messages":[{"role":"user","content":"Continue the story"}],"stream":true}`, `"type":"message_start"`},
	}
	for _, protocol := range protocols {
		testContext.Run(protocol.name, func(testContext *testing.T) {
			upstreamCalls := 0
			fixtureID := prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
				upstreamCalls++
				responseBody := "data: " + `{"id":"failed-attempt","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"discarded reasoning"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				if upstreamCalls == 2 {
					responseBody = "data: " + `{"id":"successful-attempt","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"fresh answer"}}]}` + "\n\n" +
						"data: " + `{"id":"successful-attempt","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}` + "\n\ndata: [DONE]\n\n"
				}
				response := newIndependentFixtureResponse(request, http.StatusOK, responseBody)
				response.Header.Set("Content-Type", "text/event-stream")
				return response, nil
			})
			fixtureChannel, err := channel.Get(fixtureID, context.Background())
			if err != nil {
				testContext.Fatal(err)
			}
			secondKey := appmodel.ChannelKey{ID: fixtureID + 100, ChannelID: fixtureID, Enabled: true, ChannelKey: "fixture-only-key"}
			fixtureChannel.Keys = append(fixtureChannel.Keys, secondKey)
			channel.GetCache().Set(fixtureID, *fixtureChannel)
			channel.GetKeyCache().Set(secondKey.ID, secondKey)
			testContext.Cleanup(func() { channel.GetKeyCache().Del(secondKey.ID) })
			for settingKey, settingValue := range map[appmodel.SettingKey]string{
				appmodel.SettingKeyRelayRetryCount:        "1",
				appmodel.SettingKeyRelayMaxTotalAttempts:  "2",
				appmodel.SettingKeyRetryEmptyOutput:       "true",
				appmodel.SettingKeyReasoningBufferStrategy: "buffer",
			} {
				if err := setting.SetString(settingKey, settingValue); err != nil {
					testContext.Fatal(err)
				}
			}
			recorder := httptest.NewRecorder()
			requestContext, _ := gin.CreateTestContext(recorder)
			requestContext.Request = httptest.NewRequest(http.MethodPost, protocol.requestPath, strings.NewReader(protocol.requestBody))
			requestContext.Request.Header.Set("Content-Type", "application/json")
			requestContext.Set("api_key_id", 890000)
			Handler(appmodel.EndpointTypeChat, protocol.inboundType, requestContext)
			if upstreamCalls != 2 || recorder.Code != http.StatusOK {
				testContext.Fatalf("calls=%d status=%d body=%s", upstreamCalls, recorder.Code, recorder.Body.String())
			}
			for _, forbiddenText := range []string{"discarded reasoning", "failed-attempt"} {
				if strings.Contains(recorder.Body.String(), forbiddenText) {
					testContext.Errorf("previous attempt leaked %q into response: %s", forbiddenText, recorder.Body.String())
				}
			}
			if !strings.Contains(recorder.Body.String(), "fresh answer") || !strings.Contains(recorder.Body.String(), protocol.startEvent) {
				testContext.Errorf("retry lost its answer or protocol start: %s", recorder.Body.String())
			}
			logs, cacheLock := relaylog.GetCacheAndLock()
			cacheLock.Lock()
			defer cacheLock.Unlock()
			if len(logs) != 1 || logs[0].Error != "" || len(logs[0].Attempts) != 2 {
				testContext.Fatalf("expected one successful log containing both attempts: %+v", logs)
			}
			if strings.Contains(logs[0].ResponseContent, "discarded reasoning") {
				testContext.Errorf("failed reasoning leaked into final response log: %s", logs[0].ResponseContent)
			}
		})
	}
}

func TestHandlerAdapterFallbackRespectsTotalAttemptBudget(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	for _, maximumAttempts := range []int{1, 2} {
		testContext.Run(fmt.Sprintf("budget_%d", maximumAttempts), func(testContext *testing.T) {
			fixtureID := prepareIndependentRequestFixture(testContext, nil)
			fixtureChannel, err := channel.Get(fixtureID, context.Background())
			if err != nil {
				testContext.Fatal(err)
			}
			fixtureChannel.OutboundFormatOverride = ""
			channel.GetCache().Set(fixtureID, *fixtureChannel)
			fixtureGroup, err := group.GroupGet(fixtureID, context.Background())
			if err != nil {
				testContext.Fatal(err)
			}
			fixtureGroup.OutboundFormat = ""
			group.GetCache().Set(fixtureID, *fixtureGroup)
			group.RebuildIndexes()
			if err := setting.SetString(appmodel.SettingKeyRelayMaxTotalAttempts, fmt.Sprint(maximumAttempts)); err != nil {
				testContext.Fatal(err)
			}
			fixtureClient, err := client.GetHTTPClientSystemProxy(false)
			if err != nil {
				testContext.Fatal(err)
			}
			var requestPaths []string
			fixtureClient.Transport = independentRequestTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "192.0.2.1" {
					return nil, fmt.Errorf("unexpected fixture host: %s", request.URL.Host)
				}
				requestPaths = append(requestPaths, request.URL.Path)
				return newIndependentFixtureResponse(request, http.StatusNotFound, `{"error":{"message":"fixture unsupported endpoint"}}`), nil
			})
			serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}]}`)
			if len(requestPaths) != maximumAttempts {
				testContext.Fatalf("budget=%d, actual upstream requests=%v", maximumAttempts, requestPaths)
			}
		})
	}
}

func TestHandlerOperationTimeoutReturnsErrorAndRetainsAttempts(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	upstreamCalls := 0
	prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
		upstreamCalls++
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	originalTimeout := relayUpstreamTimeout
	relayUpstreamTimeout = 50 * time.Millisecond
	testContext.Cleanup(func() { relayUpstreamTimeout = originalTimeout })
	for settingKey, value := range map[appmodel.SettingKey]string{
		appmodel.SettingKeyRelayRouteRetries:     "2",
		appmodel.SettingKeyRelayMaxTotalAttempts: "4",
	} {
		if err := setting.SetString(settingKey, value); err != nil {
			testContext.Fatal(err)
		}
	}
	recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}]}`)
	if recorder.Code != http.StatusGatewayTimeout || recorder.Body.Len() == 0 || upstreamCalls != 1 {
		testContext.Errorf("timeout must return one explicit error, not empty success: status=%d calls=%d body=%s", recorder.Code, upstreamCalls, recorder.Body.String())
	}
	logs, cacheLock := relaylog.GetCacheAndLock()
	cacheLock.Lock()
	defer cacheLock.Unlock()
	if len(logs) != 1 || len(logs[0].Attempts) != 1 || logs[0].Error == "" {
		testContext.Fatalf("timeout lost the real failed attempt: %+v", logs)
	}
}

func TestHandlerCommittedStreamFailureNeverRetries(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	upstreamCalls := 0
	prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
		upstreamCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("data: " + `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial answer"}}]}` + "\n\n" +
				"data: " + `{"error":{"message":"fixture broken stream","type":"api_error"}}` + "\n\ndata: [DONE]\n\n")),
			Request: request,
		}, nil
	})
	if err := setting.SetString(appmodel.SettingKeyRelayMaxTotalAttempts, "8"); err != nil {
		testContext.Fatal(err)
	}
	recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}],"stream":true}`)
	if upstreamCalls != 1 || strings.Count(recorder.Body.String(), "event: error\n") != 1 || strings.Contains(recorder.Body.String(), "[DONE]") {
		testContext.Fatalf("committed failure must not restart or pretend success: calls=%d body=%s", upstreamCalls, recorder.Body.String())
	}
}

func TestHandlerUncommittedStreamFailureReturnsJSON(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	for _, operationTimeout := range []bool{false, true} {
		testContext.Run(fmt.Sprintf("operation_timeout_%t", operationTimeout), func(testContext *testing.T) {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			upstreamCalls := 0
			prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
				upstreamCalls++
				response := newIndependentFixtureResponse(request, http.StatusOK, "data: {\"error\":{\"message\":\"fixture stream failure\"}}\n\n")
				response.Header.Set("Content-Type", "text/event-stream")
				if operationTimeout {
					response.Body = reader
				}
				return response, nil
			})
			originalTimeout := relayUpstreamTimeout
			relayUpstreamTimeout = 50 * time.Millisecond
			testContext.Cleanup(func() { relayUpstreamTimeout = originalTimeout })
			recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}],"stream":true}`)
			expectedStatus := http.StatusBadGateway
			if operationTimeout {
				expectedStatus = http.StatusGatewayTimeout
			}
			if recorder.Code != expectedStatus || upstreamCalls != 1 {
				testContext.Fatalf("status=%d calls=%d body=%s", recorder.Code, upstreamCalls, recorder.Body.String())
			}
			if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
				testContext.Errorf("uncommitted error retained SSE content type: %v", recorder.Header())
			}
			var payload map[string]any
			if err := jsonAPI.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				testContext.Fatalf("expected one JSON error, not an empty or mixed stream: %v; body=%s", err, recorder.Body.String())
			}
		})
	}
}

func TestHandlerRateLimitHoldStopsAtBudgetOrOperationDeadline(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	for _, maximumAttempts := range []int{1, 4} {
		testContext.Run(fmt.Sprintf("budget_%d", maximumAttempts), func(testContext *testing.T) {
			upstreamCalls := 0
			prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
				upstreamCalls++
				return newIndependentFixtureResponse(request, http.StatusTooManyRequests, `{"error":{"message":"fixture key limit"}}`), nil
			})
			for settingKey, value := range map[appmodel.SettingKey]string{
				appmodel.SettingKeyRelayMaxTotalAttempts:   fmt.Sprint(maximumAttempts),
				appmodel.SettingKeyRateLimitHoldEnabled:    "true",
				appmodel.SettingKeyRateLimitHoldInterval:   "10",
				appmodel.SettingKeyRateLimitHoldMaxWait:    "20",
			} {
				if err := setting.SetString(settingKey, value); err != nil {
					testContext.Fatal(err)
				}
			}
			originalTimeout := relayUpstreamTimeout
			relayUpstreamTimeout = 100 * time.Millisecond
			testContext.Cleanup(func() { relayUpstreamTimeout = originalTimeout })
			startedAt := time.Now()
			recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}]}`)
			if time.Since(startedAt) >= time.Second {
				testContext.Error("rate-limit wait outlived the operation deadline or an exhausted attempt budget")
			}
			expectedStatus := http.StatusBadGateway
			if maximumAttempts > 1 {
				expectedStatus = http.StatusGatewayTimeout
			}
			if recorder.Code != expectedStatus || upstreamCalls != 1 {
				testContext.Fatalf("status=%d calls=%d body=%s", recorder.Code, upstreamCalls, recorder.Body.String())
			}
			logs, cacheLock := relaylog.GetCacheAndLock()
			cacheLock.Lock()
			defer cacheLock.Unlock()
			if len(logs) != 1 || len(logs[0].Attempts) != 1 || logs[0].Attempts[0].HTTPStatus != http.StatusTooManyRequests {
				testContext.Fatalf("rate-limit exit lost the failed attempt: %+v", logs)
			}
		})
	}
}
