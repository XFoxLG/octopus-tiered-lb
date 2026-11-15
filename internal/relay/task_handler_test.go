package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/client"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/pkg/catietasks"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

// Exercise routing, retry policy, conversion, Writer and statistics together.
// The documentation IP and transport mock keep SSRF validation enabled.
func prepareTaskHandlerFixture(t *testing.T, respond independentRequestTransport) int {
	t.Helper()
	id := prepareIndependentRequestFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/openai/tasks" && req.URL.Path != "/v1/openai/tasks/fixture-task" && req.URL.Path != "/v1/openai/tasks/fixture-task/cancel" {
			return nil, fmt.Errorf("unexpected Tasks URL: %s", req.URL)
		}
		return respond(req)
	})
	ch, _ := channel.GetCache().Get(id)
	ch.ConnectionConfig = &dbmodel.ConnectionConfig{
		Version: 2, Selection: "configured", Catalog: dbmodel.ModelCatalog{Format: "manual"},
		Endpoints: []dbmodel.ChannelEndpoint{{ID: "fixture-tasks", Protocol: "chat", URL: "http://192.0.2.1", URLMode: "base", Auth: "bearer", ForwardMode: "convert", Transport: "catie_tasks", TaskAPIURL: "http://192.0.2.1/v1/openai/tasks"}},
	}
	ch.Keys = append(ch.Keys, dbmodel.ChannelKey{ID: id + 100, ChannelID: id, Enabled: true, ChannelKey: "fixture-only-key"})
	channel.GetCache().Set(id, ch)
	channel.GetKeyCache().Set(id+100, ch.Keys[1])
	t.Cleanup(func() { channel.GetKeyCache().Del(id + 100) })
	gr, _ := group.GetCache().Get(id)
	gr.HedgeEnabled, gr.HedgeDelayMs, gr.Mode = true, 0, dbmodel.GroupModeRandom
	gr.RelayRetryCount, gr.RelayRouteRetries = 3, 3
	group.GetCache().Set(id, gr)
	group.RebuildIndexes()
	for key, value := range map[dbmodel.SettingKey]string{
		dbmodel.SettingKeyRelayRetryCount: "3", dbmodel.SettingKeyRelayRouteRetries: "3", dbmodel.SettingKeyRelayMaxTotalAttempts: "12",
	} {
		if err := setting.SetString(key, value); err != nil {
			t.Fatal(err)
		}
	}
	// prepareIndependentRequestFixture checks the legacy Chat URL before calling
	// the supplied callback. Replace that transport with the same credential check
	// at the task boundary, preserving the fixture's original cleanup.
	fixtureClient, err := client.GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatal(err)
	}
	fixtureClient.Transport = independentRequestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "192.0.2.1" || req.Header.Get("Authorization") != "Bearer fixture-only-key" {
			return nil, fmt.Errorf("unexpected task origin/auth")
		}
		return respond(req)
	})
	return id
}

func addTaskFixtureBackup(t *testing.T, groupID int) {
	t.Helper()
	primary, _ := channel.GetCache().Get(groupID)
	backup := primary
	backup.ID, backup.Name = groupID+200, "unselected-backup"
	backup.ConnectionConfig = nil
	backup.Keys = []dbmodel.ChannelKey{{ID: backup.ID, ChannelID: backup.ID, Enabled: true, ChannelKey: "fixture-only-key"}}
	channel.GetCache().Set(backup.ID, backup)
	channel.GetKeyCache().Set(backup.ID, backup.Keys[0])
	t.Cleanup(func() { channel.GetCache().Del(backup.ID); channel.GetKeyCache().Del(backup.ID) })
	gr, _ := group.GetCache().Get(groupID)
	gr.Mode = dbmodel.GroupModeRoundRobin
	items := []dbmodel.GroupItem{gr.Items[0], {ChannelID: backup.ID, ModelName: "backup-fixture", Priority: 2, Weight: 1}}
	// The round-robin counter is process-wide. Choose its next position without
	// changing production selection or disabling hedge in this regression.
	first := (&balancer.RoundRobin{}).Candidates(items)[0].ChannelID
	if first == groupID {
		items[0], items[1] = items[1], items[0]
	}
	gr.Items = items
	group.GetCache().Set(groupID, gr)
	group.RebuildIndexes()
}

func TestHandlerCatieTasksQueryErrorPreservesHTTPIdentity(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var posts, gets, cancels atomic.Int64
	errorBody := `{"error":{"message":"fixture denied","type":"permission_error","code":"permission_denied"}}`
	id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/cancel"):
			cancels.Add(1)
			return newIndependentFixtureResponse(req, 200, `{}`), nil
		case req.Method == http.MethodPost:
			posts.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
		case req.Method == http.MethodGet:
			gets.Add(1)
			return newIndependentFixtureResponse(req, 403, errorBody), nil
		}
		return nil, fmt.Errorf("unexpected operation")
	})
	addTaskFixtureBackup(t, id)
	r := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"safe fixture"}]}`)
	if r.Code != 403 || r.Body.String() != errorBody {
		t.Fatalf("status/body = %d %s", r.Code, r.Body.String())
	}
	if posts.Load() != 1 || gets.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("POST/GET/cancel = %d/%d/%d", posts.Load(), gets.Load(), cancels.Load())
	}
	if got := stats.APIKeyGet(890000); got.RequestFailed != 1 || got.RequestSuccess != 0 {
		t.Fatalf("business request counted incorrectly: %+v", got)
	}
}

func TestHandlerCatieTasksStreamTerminals(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	for _, scenario := range []struct {
		name, text, reasoning, finish string
		done                          bool
		wantFailed                    bool
	}{
		{"normal", "safe final", "", "stop", true, false},
		{"filter_body", "safe partial", "", "content_filter", true, true},
		{"filter_empty", "", "", "content_filter", true, true},
		{"length_thinking", "", "safe reasoning", "max_tokens", true, true},
		{"missing_finish", "safe partial", "", "", true, true},
		{"task_running_after_sse_stop", "safe final", "", "stop", false, true},
		{"task_running_after_sse_filter", "safe partial", "", "content_filter", false, true},
		{"task_running_after_sse_length", "", "safe reasoning", "max_tokens", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var posts, cancels atomic.Int64
			id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					if strings.HasSuffix(req.URL.Path, "/cancel") {
						cancels.Add(1)
						return newIndependentFixtureResponse(req, 200, `{}`), nil
					}
					posts.Add(1)
					payload, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					if !strings.Contains(string(payload), `"model":"creative-fixture"`) {
						return nil, fmt.Errorf("model changed: %s", payload)
					}
					return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
				}
				chunk := fmt.Sprintf(`data: {"id":"fixture","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{"content":%q,"reasoning_content":%q},"finish_reason":%s}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`+"\n\n", scenario.text, scenario.reasoning, "null")
				if scenario.finish != "" {
					chunk = strings.Replace(chunk, `"finish_reason":null`, fmt.Sprintf(`"finish_reason":%q`, scenario.finish), 1)
				}
				if scenario.finish != "" {
					chunk += "data: [DONE]\n\n"
				}
				status := "completed"
				if !scenario.done {
					status = "running"
				}
				payload, _ := json.Marshal(map[string]any{"status": status, "done": scenario.done, "next_after": 1, "chunks": []string{chunk}})
				return newIndependentFixtureResponse(req, 200, string(payload)), nil
			})
			addTaskFixtureBackup(t, id)
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe fixture"}]}`))
			c.Set("api_key_id", 890000)
			trace := newRelayRequestTrace(c.Request, false)
			defer trace.close()
			c.Set(relayRequestTraceContextKey, trace)
			c.Writer = &relayCaptureResponseWriter{ResponseWriter: c.Writer, trace: trace}
			Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, c)
			wire := r.Body.String()
			if posts.Load() != 1 {
				t.Fatalf("submitted %d times: %s", posts.Load(), wire)
			}
			if scenario.text != "" && strings.Count(wire, scenario.text) != 1 {
				t.Fatalf("lost/duplicated body: %s", wire)
			}
			if scenario.finish != "" {
				finish := scenario.finish
				if finish == "max_tokens" {
					finish = "length"
				}
				if strings.Count(wire, `"finish_reason":"`+finish+`"`) != 1 || strings.Count(wire, "data: [DONE]") != 1 || strings.Contains(wire, "stream_interrupted") {
					t.Fatalf("invalid terminal wire: %s", wire)
				}
			} else if strings.Contains(wire, "[DONE]") || !strings.Contains(wire, "stream_interrupted") {
				t.Fatalf("missing terminal hidden: %s", wire)
			}
			if scenario.done && cancels.Load() != 0 || !scenario.done && cancels.Load() != 1 {
				t.Fatalf("completed task was cancelled %d times", cancels.Load())
			}
			got := stats.APIKeyGet(890000)
			if scenario.wantFailed && got.RequestFailed != 1 || !scenario.wantFailed && got.RequestSuccess != 1 {
				t.Fatalf("generation metrics: %+v", got)
			}
			d := trace.diagnosticsSnapshot()
			if d == nil || d.Task == nil || d.Network == nil || d.SelectedAttempt != 1 {
				t.Fatalf("missing selected task/network evidence: %+v", d)
			}
			if d.Task.Done != scenario.done || d.Task.TerminalReported != scenario.done || (scenario.done && d.Task.Status != "completed") || (!scenario.done && d.Task.Status != "running") {
				t.Fatalf("task state replaced by SSE finish: %+v", d.Task)
			}
			if scenario.finish != "" && (d.Network.TerminalAt == 0 || d.Attempts[0].TerminalSource != "chat") {
				t.Fatalf("SSE terminal evidence lost: %+v", d)
			}
			if scenario.finish == "content_filter" || scenario.finish == "max_tokens" {
				if d.TerminalSource != "upstream_terminal" {
					t.Fatalf("upstream filter/length rewritten as task failure: %+v", d)
				}
			}
		})
	}
}

func TestTaskResponseErrorRetainsTypedQueryAndStatus(t *testing.T) {
	original := &catietasks.ResponseError{StatusCode: 403, Body: []byte(`{"error":{"code":"permission_denied"}}`)}
	err := taskResponseError(fmt.Errorf("%w: %w", catietasks.ErrNoRetry, original))
	var query *catietasks.ResponseError
	var upstream *upstreamHTTPError
	if !errors.As(err, &query) || query != original || !errors.As(err, &upstream) || upstream.statusCode != 403 || catietasks.ErrorCode(err) != "task_query_error" {
		t.Fatalf("error identity lost: %v", err)
	}
}

func TestHandlerCatieTasksDiagnosticsPersistWithoutContent(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
		}
		return newIndependentFixtureResponse(req, 200, `{"status":"completed","done":true,"next_after":1,"chunks":["data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"safe partial\"},\"finish_reason\":\"content_filter\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\ndata: [DONE]\n\n"]}`), nil
	})
	if err := setting.SetString(dbmodel.SettingKeyRelayLogContentEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(RelayRequestTraceMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("api_key_id", 890000)
		Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, c)
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe"}]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	if strings.Count(recorder.Body.String(), "[DONE]") != 1 {
		t.Fatal(recorder.Body.String())
	}
	logs, err := relaylog.RelayLogList(context.Background(), relaylog.LogFilter{}, 1, 10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs: %v/%d", err, len(logs))
	}
	if logs[0].Diagnostics == nil || logs[0].Diagnostics.Task == nil {
		t.Fatal("list omitted task transport")
	}
	if _, err := json.Marshal(logs[0]); err != nil {
		t.Fatal(err)
	}
	if err := setting.SetString(dbmodel.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := relaylog.RelayLogSaveDBTask(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored dbmodel.RelayLog
	if err := db.GetLogDB().First(&stored, logs[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	d := stored.Diagnostics
	if d == nil || d.Task == nil || !d.Task.Done || d.Task.PollCount != 1 || d.SelectedAttempt != 1 || d.Network == nil || d.Network.FirstBodyByteAt == 0 || d.Network.TerminalAt == 0 || d.TerminalSource != "upstream_terminal" || stored.TerminationCause != "content_filter" {
		t.Fatalf("persisted diagnostics: %+v log=%+v", d, stored)
	}
	if stored.InputTokens != 11 || stored.OutputTokens != 7 || stored.RequestContent != "" || stored.ResponseContent != "" {
		t.Fatal("usage/content contract changed")
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), `"fixture-task"`) || strings.Contains(string(raw), "fixture-only-key") || strings.Contains(string(raw), "safe partial") {
		t.Fatal("task id/key/text entered diagnostics")
	}
}

func TestHandlerCatieTasksClientCancelStopsPolling(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var posts, gets, cancels atomic.Int64
	queryEntered := make(chan struct{})
	id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/cancel"):
			cancels.Add(1)
			return newIndependentFixtureResponse(req, 200, `{}`), nil
		case req.Method == http.MethodPost:
			posts.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
		default:
			gets.Add(1)
			close(queryEntered)
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
	})
	addTaskFixtureBackup(t, id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe"}]}`)).WithContext(ctx)
	c.Set("api_key_id", 890000)
	finished := make(chan struct{})
	go func() { Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, c); close(finished) }()
	select {
	case <-queryEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(6 * time.Second):
		t.Fatal("handler outlived bounded cancellation")
	}
	if posts.Load() != 1 || gets.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("POST/GET/cancel=%d/%d/%d", posts.Load(), gets.Load(), cancels.Load())
	}
	if strings.Contains(r.Body.String(), "[DONE]") {
		t.Fatal("cancel invented terminal")
	}
	if tripped, _ := balancer.IsTripped(id, id, "creative-fixture"); tripped {
		t.Fatal("cancel tripped channel")
	}
}

func TestHandlerCatieTasksReplayNeverResubmits(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	resetRelayStreamSessionsForTest(t)
	for _, finish := range []string{"stop", "content_filter", "length"} {
		t.Run(finish, func(t *testing.T) {
			var posts, gets, cancels atomic.Int64
			id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(req.URL.Path, "/cancel"):
					cancels.Add(1)
					return newIndependentFixtureResponse(req, 200, `{}`), nil
				case req.Method == http.MethodPost && req.URL.Path == "/v1/openai/tasks":
					posts.Add(1)
					return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v1/openai/tasks/fixture-task":
					gets.Add(1)
					chunk := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"safe replay answer\"},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\ndata: [DONE]\n\n", finish)
					body, _ := json.Marshal(map[string]any{"status": "completed", "done": true, "next_after": 1, "chunks": []string{chunk}})
					return newIndependentFixtureResponse(req, 200, string(body)), nil
				default:
					return nil, fmt.Errorf("unexpected route on replay: %s %s", req.Method, req.URL.Path)
				}
			})
			addTaskFixtureBackup(t, id)
			if err := setting.SetString(dbmodel.SettingKeyStreamSessionReplayEnabled, "true"); err != nil {
				t.Fatal(err)
			}
			requestBody := fmt.Sprintf(`{"conversation_id":%q,"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe"}]}`, "task-replay-"+finish)
			first := serveIndependentFixtureRequest(requestBody)
			replay := serveIndependentFixtureRequest(requestBody)
			if first.Code != 200 || replay.Code != 200 || first.Body.String() != replay.Body.String() {
				t.Fatalf("replay changed bytes: first=%d/%s replay=%d/%s", first.Code, first.Body, replay.Code, replay.Body)
			}
			wire := replay.Body.String()
			if strings.Count(wire, "safe replay answer") != 1 || strings.Count(wire, `"finish_reason":"`+finish+`"`) != 1 || strings.Count(wire, "data: [DONE]") != 1 || strings.Contains(wire, "stream_interrupted") {
				t.Fatalf("invalid replay terminal: %s", wire)
			}
			if posts.Load() != 1 || gets.Load() != 1 || cancels.Load() != 0 {
				t.Fatalf("replay re-executed task: POST/GET/cancel=%d/%d/%d", posts.Load(), gets.Load(), cancels.Load())
			}
			got := stats.APIKeyGet(890000)
			if got.RequestSuccess+got.RequestFailed != 1 || got.InputToken != 11 || got.OutputToken != 7 {
				t.Fatalf("replay counted another generation: %+v", got)
			}
		})
	}
}

func TestHandlerCatieTasksWriterFailureStopsPolling(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var posts, gets, cancels atomic.Int64
	id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/cancel"):
			cancels.Add(1)
			return newIndependentFixtureResponse(req, 200, `{}`), nil
		case req.Method == http.MethodPost:
			posts.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
		default:
			gets.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"status":"running","done":false,"next_after":1,"chunks":["data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"safe body\"},\"finish_reason\":null}]}\n\n"]}`), nil
		}
	})
	addTaskFixtureBackup(t, id)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe"}]}`))
	c.Set("api_key_id", 890000)
	trace := newRelayRequestTrace(c.Request, false)
	defer trace.close()
	c.Set(relayRequestTraceContextKey, trace)
	c.Writer = &relayCaptureResponseWriter{ResponseWriter: taskWriteFailureSink{c.Writer}, trace: trace}
	Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, c)
	if posts.Load() != 1 || gets.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("Writer failure kept generating: POST/GET/cancel=%d/%d/%d", posts.Load(), gets.Load(), cancels.Load())
	}
	if strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatal("Writer failure fabricated completion")
	}
	if tripped, _ := balancer.IsTripped(id, id, "creative-fixture"); tripped {
		t.Fatal("Writer failure tripped channel")
	}
	if trace.diagnostics.TerminalSource != "delivery_error" || trace.diagnostics.Task == nil || trace.diagnostics.Task.CancelState != "confirmed" {
		t.Fatalf("Writer failure attribution: %+v", trace.diagnostics)
	}
}

func TestHandlerCatieTasksFlushFailureStopsPolling(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	var posts, gets, cancels atomic.Int64
	id := prepareTaskHandlerFixture(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/cancel"):
			cancels.Add(1)
			return newIndependentFixtureResponse(req, 200, `{}`), nil
		case req.Method == http.MethodPost:
			posts.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"id":"fixture-task"}`), nil
		default:
			gets.Add(1)
			return newIndependentFixtureResponse(req, 200, `{"status":"running","done":false,"next_after":1,"chunks":["data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"safe body\"},\"finish_reason\":null}]}\n\n"]}`), nil
		}
	})
	addTaskFixtureBackup(t, id)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(taskFlushFailureSink{recorder})
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"safe"}]}`)).WithContext(ctx)
	c.Set("api_key_id", 890000)
	trace := newRelayRequestTrace(c.Request, false)
	defer trace.close()
	c.Set(relayRequestTraceContextKey, trace)
	c.Writer = &relayCaptureResponseWriter{ResponseWriter: c.Writer, trace: trace}
	Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, c)
	if posts.Load() != 1 || gets.Load() != 1 || cancels.Load() != 1 || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("flush failure kept generating or invented terminal: %d/%d/%d %s", posts.Load(), gets.Load(), cancels.Load(), recorder.Body)
	}
	if trace.diagnostics.TerminalSource != "delivery_error" || trace.diagnostics.Task == nil || trace.diagnostics.Task.CancelState != "confirmed" {
		t.Fatalf("flush failure attribution: %+v", trace.diagnostics)
	}
}

type taskFlushFailureSink struct{ *httptest.ResponseRecorder }

func (w taskFlushFailureSink) FlushError() error { return io.ErrClosedPipe }

type taskWriteFailureSink struct{ gin.ResponseWriter }

func (w taskWriteFailureSink) Write([]byte) (int, error)   { return 0, io.ErrClosedPipe }
func (w taskWriteFailureSink) Unwrap() http.ResponseWriter { return w.ResponseWriter }
