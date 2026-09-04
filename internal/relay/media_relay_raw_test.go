package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestBuildRawPassthroughUpstreamURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		baseURL  string
		reqPath  string
		reqQuery string
		want     string
	}{
		{
			name:     "client path with query preserved",
			baseURL:  "https://api.example.com",
			reqPath:  "/v1/images/generations",
			reqQuery: "foo=1&bar=2",
			want:     "https://api.example.com/v1/images/generations?foo=1&bar=2",
		},
		{
			name:     "base url ending in v1 does not duplicate version",
			baseURL:  "https://api.example.com/v1",
			reqPath:  "/v1/images/edits",
			reqQuery: "",
			want:     "https://api.example.com/v1/images/edits",
		},
		{
			name:     "arbitrary non-v1 client path preserved as-is",
			baseURL:  "https://api.example.com",
			reqPath:  "/custom/image/path",
			reqQuery: "",
			want:     "https://api.example.com/custom/image/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, tt.reqPath, strings.NewReader("{}"))
			ctx.Request.URL.RawQuery = tt.reqQuery

			got, err := buildRawPassthroughUpstreamURL(tt.baseURL, ctx)
			if err != nil {
				t.Fatalf("buildRawPassthroughUpstreamURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("buildRawPassthroughUpstreamURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestForwardMediaRequestJSONRawProviderKeepsClientPathAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	var (
		mu       sync.Mutex
		gotPath  string
		gotQuery string
		gotBody  string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		mu.Lock()
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotBody = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1713167890,"data":[{"url":"https://cdn.example.com/gen/abc"}]}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	// 客户端直接请求上游真实的自定义路径（非 /v1/images/* 规范路径），
	// raw 模式应原样保留该路径与查询串。
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits?priority=high", strings.NewReader(
		`{"model":"sensenova-u1.5-lite","images":[{"image_url":"data:image/png;base64,iVBOR"}],"prompt":"雪山","watermark":true,"prompt_extend":true}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	group := dbmodel.Group{EndpointProvider: "raw"}
	cfg := mediaEndpointConfig{UpstreamPath: "/v1/images/edits", MultipartInput: true}

	status, err := forwardMediaRequestJSON(
		ctx,
		cfg,
		group,
		&dbmodel.Channel{BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}}},
		"sk-test",
		[]byte(`{"model":"sensenova-u1.5-lite","images":[{"image_url":"data:image/png;base64,iVBOR"}],"prompt":"雪山","watermark":true,"prompt_extend":true}`),
		"sensenova-u1.5-lite",
		"sensenova-u1.5-lite", // 分组模型映射后模型名不变
		false,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("forwardMediaRequestJSON() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v1/images/edits" {
		t.Fatalf("upstream path = %q, want client path /v1/images/edits", gotPath)
	}
	if gotQuery != "priority=high" {
		t.Fatalf("upstream query = %q, want priority=high", gotQuery)
	}
	if !strings.Contains(gotBody, `"watermark":true`) || !strings.Contains(gotBody, `"prompt_extend":true`) {
		t.Fatalf("upstream body should keep original fields verbatim, got = %s", gotBody)
	}
}

func TestForwardMediaRequestJSONRawProviderRewritesModelOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	var (
		mu      sync.Mutex
		gotBody string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		mu.Lock()
		gotBody = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aGk="}]}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"proxy-model","prompt":"海獭"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	group := dbmodel.Group{EndpointProvider: "raw"}
	cfg := mediaEndpointConfig{UpstreamPath: "/v1/images/generations"}

	status, err := forwardMediaRequestJSON(
		ctx,
		cfg,
		group,
		&dbmodel.Channel{BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}}},
		"sk-test",
		[]byte(`{"model":"proxy-model","prompt":"海獭"}`),
		"proxy-model",
		"upstream-real-model",
		false,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("forwardMediaRequestJSON() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(gotBody, `"model":"upstream-real-model"`) {
		t.Fatalf("upstream body model should be rewritten, got = %s", gotBody)
	}
	if !strings.Contains(gotBody, `"prompt":"海獭"`) {
		t.Fatalf("upstream body should keep other fields, got = %s", gotBody)
	}
}

func TestExtractModelFromMultipartFallsBackToJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(
		`{"model":"sensenova-u1.5-lite","images":[{"image_url":"data:image/png;base64,iVBOR"}],"prompt":"雪山"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	model, body, streamRequested, err := extractModelFromMultipart(ctx)
	if err != nil {
		t.Fatalf("extractModelFromMultipart() error = %v", err)
	}
	if model != "sensenova-u1.5-lite" {
		t.Fatalf("model = %q, want sensenova-u1.5-lite", model)
	}
	if streamRequested {
		t.Fatal("streamRequested = true, want false")
	}
	if !strings.Contains(string(body), `"prompt":"雪山"`) {
		t.Fatalf("body should be preserved for JSON forwarding, got = %s", string(body))
	}
}

func TestForwardMediaRequestRoutesJSONBodyOnMultipartEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	var (
		mu      sync.Mutex
		gotBody string
		gotCT   string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		mu.Lock()
		gotBody = string(body)
		gotCT = r.Header.Get("Content-Type")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://cdn.example.com/x"}]}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(
		`{"model":"sensenova-u1.5-lite","images":[{"image_url":"data:image/png;base64,iVBOR"}],"prompt":"雪山"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	cfg := getMediaEndpointConfig(MediaEndpointImageEdit)
	group := dbmodel.Group{EndpointProvider: "raw"}

	model, body, _, err := extractModelFromRequest(ctx, cfg)
	if err != nil {
		t.Fatalf("extractModelFromRequest() error = %v", err)
	}
	if model != "sensenova-u1.5-lite" {
		t.Fatalf("model = %q, want sensenova-u1.5-lite", model)
	}

	status, err := forwardMediaRequest(
		ctx,
		cfg,
		group,
		&dbmodel.Channel{BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}}},
		"sk-test",
		body,
		"sensenova-u1.5-lite",
		"sensenova-u1.5-lite",
		false,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("forwardMediaRequest() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCT != "application/json" {
		t.Fatalf("upstream Content-Type = %q, want application/json", gotCT)
	}
	if !strings.Contains(gotBody, `"prompt":"雪山"`) || !strings.Contains(gotBody, `"image_url"`) {
		t.Fatalf("upstream body should be the original JSON, got = %s", gotBody)
	}
}
