package catietasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func allowTaskTestServer(t *testing.T) {
	t.Helper()
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
}

func taskRequest(t *testing.T, submitURL, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, submitURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer synthetic")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestDoStreamsTaskChunksExactlyOnce(t *testing.T) {
	allowTaskTestServer(t)
	var submitCount, queryCount, cancelCount atomic.Int32
	var submittedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelCount.Add(1)
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/openai/tasks":
			submitCount.Add(1)
			raw, _ := io.ReadAll(r.Body)
			submittedBody = string(raw)
			w.Header().Set("X-Request-ID", "task-request")
			_, _ = io.WriteString(w, `{"id":"task-1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/openai/tasks/task-1":
			queryCount.Add(1)
			if r.URL.Query().Get("after") == "0" {
				_, _ = io.WriteString(w, `{"chunks":["data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"],"next_after":1,"done":false,"status":"running"}`)
				return
			}
			_, _ = io.WriteString(w, `{"chunks":["data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n","data: [DONE]\n\n"],"next_after":3,"done":true,"status":"completed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	state := NewState()
	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{"model":"test"}`), Config{Stream: true}, state)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	want := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	if string(body) != want {
		t.Fatalf("task chunks changed:\n got %q\nwant %q", body, want)
	}
	if submitCount.Load() != 1 || queryCount.Load() != 2 {
		t.Fatalf("submit/query count = %d/%d", submitCount.Load(), queryCount.Load())
	}
	if submittedBody != `{"model":"test"}` {
		t.Fatalf("submission body changed: %s", submittedBody)
	}
	if cancelCount.Load() != 0 {
		t.Fatalf("completed task was canceled %d times", cancelCount.Load())
	}
	snapshot := state.Snapshot()
	if snapshot.SubmissionState != "accepted" || !snapshot.Done || snapshot.Cursor != 3 ||
		snapshot.PollCount != 2 || snapshot.Status != "completed" || snapshot.RequestID != "task-request" {
		t.Fatalf("task state = %+v", snapshot)
	}
}

func TestDoNonStreamReturnsResultAndRetriesQuery(t *testing.T) {
	allowTaskTestServer(t)
	var queryCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"task-2"}`)
		case r.Method == http.MethodGet:
			if queryCount.Add(1) == 1 {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"chunks":[],"next_after":0,"done":true,"status":"completed","result":{"ok":true}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	state := NewState()
	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{}, state)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != `{"ok":true}` {
		t.Fatalf("non-stream result = %s", body)
	}
	snapshot := state.Snapshot()
	if snapshot.PollCount != 2 || snapshot.QueryRetryCount != 1 {
		t.Fatalf("query retry state = %+v", snapshot)
	}
}

func TestDoRejectsCursorJumpWithoutResubmission(t *testing.T) {
	allowTaskTestServer(t)
	var submitCount, queryCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.Method == http.MethodPost:
			submitCount.Add(1)
			_, _ = io.WriteString(w, `{"id":"task-3"}`)
		case r.Method == http.MethodGet:
			queryCount.Add(1)
			_, _ = io.WriteString(w, `{"chunks":["one"],"next_after":5,"done":false,"status":"running"}`)
		}
	}))
	t.Cleanup(server.Close)

	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{Stream: true}, NewState())
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr == nil {
		t.Fatal("cursor jump was accepted")
	}
	if !errors.Is(readErr, ErrNoRetry) || !strings.Contains(readErr.Error(), "cursor skipped") {
		t.Fatalf("cursor error = %v", readErr)
	}
	if submitCount.Load() != 1 || queryCount.Load() != 1 {
		t.Fatalf("cursor failure retried work: submit=%d query=%d", submitCount.Load(), queryCount.Load())
	}
}

func TestDoSubmissionWithoutTaskIDIsUnknownAndNeverRetried(t *testing.T) {
	allowTaskTestServer(t)
	var submitCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		submitCount.Add(1)
		_, _ = io.WriteString(w, `{"status":"queued"}`)
	}))
	t.Cleanup(server.Close)

	_, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{Stream: true}, NewState())
	if !errors.Is(err, ErrSubmissionUnknown) || !errors.Is(err, ErrNoRetry) {
		t.Fatalf("submission error = %v", err)
	}
	if submitCount.Load() != 1 {
		t.Fatalf("submission retried %d times", submitCount.Load())
	}
}

func TestDoClientCloseCancelsKnownTaskState(t *testing.T) {
	allowTaskTestServer(t)
	var cancelCount atomic.Int32
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelCount.Add(1)
			canceled <- struct{}{}
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"task-5"}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"chunks":[],"next_after":0,"done":false,"status":"running"}`)
		}
	}))
	t.Cleanup(server.Close)

	state := NewState()
	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{Stream: true}, state)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatalf("task cancellation was not requested; state=%+v", state.Snapshot())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && state.Snapshot().CancelState != "confirmed" {
		time.Sleep(10 * time.Millisecond)
	}
	if cancelCount.Load() != 1 || state.Snapshot().CancelState != "confirmed" {
		t.Fatalf("cancel count/state = %d/%+v", cancelCount.Load(), state.Snapshot())
	}
}

func TestTaskTerminalErrorOmitsUnknownHTTPStatus(t *testing.T) {
	err := (&terminalError{status: "expired"}).Error()
	if strings.Contains(err, "HTTP 0") || !strings.Contains(err, "task expired") {
		t.Fatalf("terminal error = %q", err)
	}
}

func TestDoStreamTerminalDoesNotCancelTask(t *testing.T) {
	allowTaskTestServer(t)
	var cancelCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelCount.Add(1)
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"task-terminal"}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"chunks":["data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"content_filter\"}]}\n\n"],"next_after":1,"done":true,"status":"failed","status_code":200,"error":"content_filter"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{Stream: true}, NewState())
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "content_filter") {
		t.Fatalf("stream chunks were not delivered before the terminal error: body=%q err=%v", body, readErr)
	}
	if readErr == nil {
		t.Fatal("terminal task error was not surfaced after its final chunks")
	}
	if cancelCount.Load() != 0 {
		t.Fatalf("terminal task was canceled %d times", cancelCount.Load())
	}
}

func TestDoRejectsHTMLAndBadJSONWithoutRetry(t *testing.T) {
	allowTaskTestServer(t)
	for name, responseBody := range map[string]string{
		"html":     `<html>gateway error</html>`,
		"bad json": `{"chunks":`,
	} {
		t.Run(name, func(t *testing.T) {
			var queryCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
					_, _ = io.WriteString(w, `{"ok":true}`)
				case r.Method == http.MethodPost:
					_, _ = io.WriteString(w, `{"id":"task-invalid"}`)
				case r.Method == http.MethodGet:
					queryCount.Add(1)
					_, _ = io.WriteString(w, responseBody)
				}
			}))
			t.Cleanup(server.Close)

			_, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/v1/openai/tasks", `{}`), Config{}, NewState())
			if !errors.Is(err, ErrNoRetry) {
				t.Fatalf("invalid query error = %v", err)
			}
			if queryCount.Load() != 1 {
				t.Fatalf("invalid %s response retried %d times", name, queryCount.Load())
			}
		})
	}
}

type taskTestTransport func(*http.Request) (*http.Response, error)

func (f taskTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func taskTestResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestTaskSubmissionReadsSlowHTTPBodyBeforeCancel(t *testing.T) {
	allowTaskTestServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"done":true,"status":"completed","result":{"ok":true}}`)
			return
		}
		w.Header().Set("CF-Ray", "cf-submission")
		w.Header().Set("Rndr-Id", "render-submission")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = io.WriteString(w, `{"id":"slow-id"}`)
	}))
	t.Cleanup(server.Close)
	state := NewState()
	response, err := Do(context.Background(), server.Client(), taskRequest(t, server.URL+"/tasks", `{}`), Config{}, state)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if state.Snapshot().RequestID != "" || state.Snapshot().CFRay != "cf-submission" || state.Snapshot().RndrID != "render-submission" {
		t.Fatalf("different observed identifiers were conflated: %+v", state.Snapshot())
	}
}

func TestTaskNonStreamPreservesProviderFailureBody(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "expired"} {
		t.Run(status, func(t *testing.T) {
			var posts int
			want := `{"error":{"code":"insufficient_user_quota","message":"synthetic provider error"}}`
			client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					posts++
					return taskTestResponse(r, 200, `{"id":"t"}`), nil
				}
				return taskTestResponse(r, 200, fmt.Sprintf(`{"done":true,"status":%q,"status_code":403,"result":%s}`, status, want)), nil
			})}
			response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{}, NewState())
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != 403 || string(body) != want || posts != 1 {
				t.Fatalf("status/body/posts = %d/%s/%d", response.StatusCode, body, posts)
			}
		})
	}
}

func TestTaskQueryRetriesSameCursorWithFakeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		posts, queries := 0, 0
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				posts++
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			queries++
			if r.URL.Query().Get("after") != "0" {
				t.Fatal("retry cursor changed")
			}
			if queries <= 2 {
				return taskTestResponse(r, 503, `{"error":"temporary"}`), nil
			}
			return taskTestResponse(r, 200, `{"done":true,"status":"completed","result":{"ok":true}}`), nil
		})}
		state := NewState()
		response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{}, state)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if time.Since(start) != 3*time.Second || posts != 1 || queries != 3 || state.Snapshot().QueryRetryCount != 2 {
			t.Fatalf("retry timing/count = %s/%d/%d/%+v", time.Since(start), posts, queries, state.Snapshot())
		}
	})
}

func TestTaskThirtyMinuteLimitAndEarlierBudget(t *testing.T) {
	for _, budget := range []time.Duration{TotalTimeout, 4 * time.Second} {
		t.Run(budget.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				posts, cancels := 0, 0
				client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/cancel") {
						cancels++
						return taskTestResponse(r, 200, `{}`), nil
					}
					if r.Method == http.MethodPost {
						posts++
						return taskTestResponse(r, 200, `{"id":"t"}`), nil
					}
					return taskTestResponse(r, 200, `{"done":false,"status":"running","next_after":0,"chunks":[]}`), nil
				})}
				state := NewState()
				_, err := Do(ctx, client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{}, state)
				if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrNoRetry) || time.Since(start) != budget || posts != 1 || cancels != 1 || !state.Snapshot().TimedOut {
					t.Fatalf("deadline result: err=%v elapsed=%s posts=%d cancels=%d state=%+v", err, time.Since(start), posts, cancels, state.Snapshot())
				}
			})
		})
	}
}

func TestTaskCloseUnblocksPipeAndWaitsForBoundedCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cancels atomic.Int32
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/cancel") {
				cancels.Add(1)
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			if r.Method == http.MethodPost {
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			return taskTestResponse(r, 200, `{"chunks":["blocked chunk"],"next_after":1,"done":false,"status":"running"}`), nil
		})}
		state := NewState()
		response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, state)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		start := time.Now()
		_ = response.Body.Close()
		if time.Since(start) != cancelTimeout || cancels.Load() != 1 || state.Snapshot().CancelState != "unconfirmed" {
			t.Fatalf("close cleanup = %s/%d/%+v", time.Since(start), cancels.Load(), state.Snapshot())
		}
		_ = response.Body.Close()
		if cancels.Load() != 1 {
			t.Fatal("duplicate cancellation")
		}
	})
}

func TestTaskParentCancelUnblocksUnreadPipe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var cancels atomic.Int32
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/cancel") {
				cancels.Add(1)
				return taskTestResponse(r, 200, `{}`), nil
			}
			if r.Method == http.MethodPost {
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			return taskTestResponse(r, 200, `{"chunks":["blocked"],"next_after":1,"done":false,"status":"running"}`), nil
		})}
		state := NewState()
		response, err := Do(ctx, client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, state)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cancel()
		synctest.Wait()
		_, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if !errors.Is(readErr, context.Canceled) || cancels.Load() != 1 || state.Snapshot().CancelState != "confirmed" {
			t.Fatalf("cancel: %v/%d/%+v", readErr, cancels.Load(), state.Snapshot())
		}
	})
}

func TestTaskCompletedFinalBatchCloseDoesNotCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cancels atomic.Int32
		state := NewState()
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/cancel") {
				cancels.Add(1)
				return taskTestResponse(r, 200, `{}`), nil
			}
			if r.Method == http.MethodPost {
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			return taskTestResponse(r, 200, `{"chunks":["final chunk"],"next_after":1,"done":true,"status":"completed"}`), nil
		})}
		response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, state)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		_ = response.Body.Close()
		if cancels.Load() != 0 || state.Snapshot().CancelledAt != 0 || state.Snapshot().TimedOut {
			t.Fatal("task was canceled after terminal envelope")
		}
	})
}

func TestTaskBatchChunksAndEmptyPollsKeepExactBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cursors []string
		queryCount := 0
		want := "data: {\"safe\":true}\n\ndata: [DONE]\n\n"
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			cursors = append(cursors, r.URL.Query().Get("after"))
			queryCount++
			switch queryCount {
			case 1:
				return taskTestResponse(r, 200, `{"chunks":[],"next_after":0,"done":false,"status":"queued"}`), nil
			case 2:
				return taskTestResponse(r, 200, `{"chunks":["data: {\"safe\":"],"next_after":1,"done":false,"status":"running"}`), nil
			default:
				encoded, _ := json.Marshal(map[string]any{"chunks": []string{"true}\n\ndata: [DONE]\n\n"}, "next_after": 2, "done": true, "status": "completed"})
				return taskTestResponse(r, 200, string(encoded)), nil
			}
		})}
		response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, NewState())
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != want || strings.Join(cursors, ",") != "0,0,1" {
			t.Fatalf("batch result %q/%v/%v", body, err, cursors)
		}
	})
}

func TestTaskInvalidCursorAndOversizeAreNotRetried(t *testing.T) {
	for name, body := range map[string]string{"backwards": `{"chunks":[],"next_after":-1,"done":false,"status":"running"}`, "duplicate chunk": `{"chunks":["repeated"],"next_after":0,"done":false,"status":"running"}`, "oversize": strings.Repeat("x", maxJSONBody+1)} {
		t.Run(name, func(t *testing.T) {
			var queries atomic.Int32
			client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					return taskTestResponse(r, 200, `{"id":"t"}`), nil
				}
				queries.Add(1)
				return taskTestResponse(r, 200, body), nil
			})}
			response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, NewState())
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if !errors.Is(err, ErrNoRetry) || queries.Load() != 1 {
				t.Fatalf("bad envelope retried: %v/%d", err, queries.Load())
			}
		})
	}
}

func TestTaskSubmitRedirectNeverReposts(t *testing.T) {
	var calls int
	client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		response := taskTestResponse(r, 307, `redirect`)
		response.Header.Set("Location", "http://192.0.2.2/tasks")
		return response, nil
	})}
	response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{}, NewState())
	if err != nil || response.StatusCode != 307 || calls != 1 {
		t.Fatalf("redirect outcome %v/%v/%d", response, err, calls)
	}
	_ = response.Body.Close()
}

func TestTaskRetryAfterBoundaries(t *testing.T) {
	now := time.Now()
	if parseRetryAfter("9223372036854775807", now) <= TotalTimeout || parseRetryAfter("-1", now) != 0 || parseRetryAfter(now.Add(20*time.Second).Format(http.TimeFormat), now) <= 19*time.Second {
		t.Fatal("Retry-After parsed incorrectly or overflowed")
	}
}

func TestTaskCloseFromTwoConsumersIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cancels atomic.Int32
		client := &http.Client{Transport: taskTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/cancel") {
				cancels.Add(1)
				return taskTestResponse(r, 200, `{}`), nil
			}
			if r.Method == http.MethodPost {
				return taskTestResponse(r, 200, `{"id":"t"}`), nil
			}
			return taskTestResponse(r, 200, `{"chunks":[],"next_after":0,"done":false,"status":"running"}`), nil
		})}
		response, err := Do(context.Background(), client, taskRequest(t, "http://192.0.2.1/tasks", `{}`), Config{Stream: true}, NewState())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Go(func() { _ = response.Body.Close() })
		}
		wg.Wait()
		if cancels.Load() != 1 {
			t.Fatal("concurrent close duplicated cancellation")
		}
	})
}
