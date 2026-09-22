package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// failingGinWriter 模拟客户端断开后的写失败（broken pipe）。
type failingGinWriter struct {
	gin.ResponseWriter
}

func (w *failingGinWriter) Write([]byte) (int, error) {
	return 0, errors.New("broken pipe")
}

func (w *failingGinWriter) WriteString(string) (int, error) {
	return 0, errors.New("broken pipe")
}

// newMediaWriteTestContext 构造带（可选已取消）客户端上下文与失败 Writer 的测试上下文。
func newMediaWriteTestContext(clientContext context.Context) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	if clientContext != nil {
		request = request.WithContext(clientContext)
	}
	ginContext.Request = request
	// gin.CreateTestContext 已把 Writer 设为 recorder 自身；包一层失败 Writer。
	ginContext.Writer = &failingGinWriter{ResponseWriter: ginContext.Writer}
	return ginContext
}

func upstreamJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// gatedGinWriter 模拟「上游已响应、客户端已断开」后写回必然失败的客户端 Writer。
// 首次写阻塞直到 released 关闭——由上游 handler 在取消客户端上下文之后触发，
// 保证取消时序确定：循环预检查（上下文存活）→ 转发 → 上游取消客户端 → 写回失败。
type gatedGinWriter struct {
	gin.ResponseWriter
	released <-chan struct{}
}

func (w *gatedGinWriter) Write(p []byte) (int, error) {
	<-w.released
	return 0, errors.New("broken pipe")
}

func (w *gatedGinWriter) WriteString(s string) (int, error) {
	<-w.released
	return 0, errors.New("broken pipe")
}

func TestMediaWriteFailureWithDisconnectedClientReturnsSentinel(t *testing.T) {
	clientContext, cancel := context.WithCancel(context.Background())
	cancel() // 客户端已断开：请求上下文已取消

	ginContext := newMediaWriteTestContext(clientContext)

	statusCode, writeErr := handleJSONResponse(ginContext, upstreamJSONResponse(http.StatusOK, `{"ok":true}`))
	if statusCode != 0 {
		t.Fatalf("status = %d, want 0", statusCode)
	}
	if !errors.Is(writeErr, errClientDisconnected) {
		t.Fatalf("error = %v, want errClientDisconnected", writeErr)
	}
	if writeErr.Error() != "client disconnected" {
		t.Fatalf("error text = %q, want exact %q (frontend matches the literal)", writeErr.Error(), "client disconnected")
	}
}

func TestMediaSSEWriteFailureWithDisconnectedClientReturnsSentinel(t *testing.T) {
	clientContext, cancel := context.WithCancel(context.Background())
	cancel()

	ginContext := newMediaWriteTestContext(clientContext)
	response := upstreamJSONResponse(http.StatusOK, "")
	response.Header.Set("Content-Type", "text/event-stream")
	response.Body = io.NopCloser(strings.NewReader("data: {\"delta\":\"hi\"}\n\n"))

	_, writeErr := handleSSEResponse(ginContext, response)
	if !errors.Is(writeErr, errClientDisconnected) {
		t.Fatalf("error = %v, want errClientDisconnected", writeErr)
	}
	if writeErr.Error() != "client disconnected" {
		t.Fatalf("error text = %q, want exact %q", writeErr.Error(), "client disconnected")
	}
}

func TestMediaBinaryWriteFailureWithDisconnectedClientReturnsSentinel(t *testing.T) {
	clientContext, cancel := context.WithCancel(context.Background())
	cancel()

	ginContext := newMediaWriteTestContext(clientContext)
	response := upstreamJSONResponse(http.StatusOK, "binary-bytes")
	response.Header.Set("Content-Type", "audio/mpeg")

	_, writeErr := handleBinaryResponse(ginContext, response)
	if !errors.Is(writeErr, errClientDisconnected) {
		t.Fatalf("error = %v, want errClientDisconnected", writeErr)
	}
}

// 客户端仍在时，写失败必须保留原始错误细节，不得误判为断连。
func TestMediaWriteFailureWithConnectedClientKeepsOriginalError(t *testing.T) {
	ginContext := newMediaWriteTestContext(context.Background())

	_, writeErr := handleJSONResponse(ginContext, upstreamJSONResponse(http.StatusOK, `{"ok":true}`))
	if writeErr == nil || errors.Is(writeErr, errClientDisconnected) {
		t.Fatalf("error = %v, want original wrapped write error (not errClientDisconnected)", writeErr)
	}
	if !strings.Contains(writeErr.Error(), "failed to stream response") {
		t.Fatalf("error text = %q, want wrapped original detail", writeErr.Error())
	}
}

// writeErrorOutcome 的判定矩阵：客户端上下文取消与否决定归因。
func TestWriteErrorOutcomeClassification(t *testing.T) {
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	ginCanceled := newMediaWriteTestContext(canceledContext)
	ginConnected := newMediaWriteTestContext(context.Background())

	if got := writeErrorOutcome(ginCanceled, errors.New("broken pipe")); !errors.Is(got, errClientDisconnected) {
		t.Fatalf("canceled client: got %v, want errClientDisconnected", got)
	}
	original := errors.New("upstream reset")
	if got := writeErrorOutcome(ginConnected, original); got != original {
		t.Fatalf("connected client: got %v, want original error unchanged", got)
	}
}

// 集成回归：客户端断开连接的媒体请求即使重复触发，也绝不能污染熔断计数。
// 修复前的缺陷是写失败被 ClassifyRelayError 归因为网络错误/已写出，进而
// RecordFailure 累计到阈值后熔断健康渠道（连续 5 次即触发，默认阈值 5）。
// 这里连续跑 6 次断连请求，断言熔断器始终未跳闸。
func TestMediaClientDisconnectDoesNotTripCircuitBreaker(t *testing.T) {
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	if err := db.InitDB("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", testName), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	// 上游正常返回 200 + 可流式正文；失败只发生在向已断开的客户端写回时。
	var upstreamHits atomic.Int64
	// pendingClientCancel 让上游 handler 在响应写完后取消当前请求的客户端上下文，
	// 模拟「上游仍在跑、客户端中途断开」的真实 Re-roll 时序。
	var pendingClientCancel atomic.Pointer[context.CancelFunc]
	// releaseWriteCh 由上游 handler 在取消完成后关闭，解除代理写回的阻塞。
	releaseWriteCh := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://cdn.example.com/img.png"}]}`))
		if cancel := pendingClientCancel.Swap(nil); cancel != nil {
			(*cancel)()
		}
		close(releaseWriteCh)
	}))
	defer upstream.Close()

	testChannel := dbmodel.Channel{
		Name:     "media-disconnect-test",
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		Model:    "flux-test",
		BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}},
		Keys: []dbmodel.ChannelKey{{
			Enabled:    true,
			ChannelKey: "sk-media-disconnect",
		}},
	}
	if err := channel.Create(&testChannel, context.Background()); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	testGroup := dbmodel.Group{
		// 媒体路由按「请求模型名 → 分组名/正则」精确匹配（GroupGetEnabledMapByEndpoint），
		// 分组名必须等于请求模型名才能命中。
		Name:         "flux-test",
		EndpointType: dbmodel.EndpointTypeImageGeneration,
		Mode:         dbmodel.GroupModeFailover,
		Items: []dbmodel.GroupItem{{
			ChannelID: testChannel.ID,
			ModelName: "flux-test",
		}},
	}
	if err := group.GroupCreate(&testGroup, context.Background()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	runDisconnectedRequest := func(t *testing.T) {
		t.Helper()
		clientContext, cancelClient := context.WithCancel(context.Background())
		pendingClientCancel.Store(&cancelClient)
		releaseWriteCh = make(chan struct{})

		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(recorder)
		ginContext.Writer = &gatedGinWriter{ResponseWriter: ginContext.Writer, released: releaseWriteCh}
		ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations",
			strings.NewReader(`{"model":"flux-test","prompt":"cat"}`)).WithContext(clientContext)
		ginContext.Request.Header.Set("Content-Type", "application/json")
		ginContext.Set("api_key_id", 0)

		MediaHandler(MediaEndpointImageGeneration, ginContext)
		cancelClient() // 兜底：若上游未触发取消，避免上下文泄漏
	}

	const attempts = 6 // 默认熔断阈值为 5；若豁免缺失，6 次失败必然熔断
	for i := 0; i < attempts; i++ {
		runDisconnectedRequest(t)
	}

	if hits := upstreamHits.Load(); hits == 0 {
		t.Fatalf("upstream was never reached; test harness is broken")
	}
	tripped, _ := balancer.IsTripped(testChannel.ID, testChannel.Keys[0].ID, "flux-test")
	if tripped {
		t.Fatalf("circuit breaker tripped after %d client-disconnect requests; exemption missing", attempts)
	}
}
