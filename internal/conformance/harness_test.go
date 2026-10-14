package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// 对拍统一使用的占位上游地址与凭据，与两个 oracle 模块保持一致。
const (
	ourBaseURL = "https://upstream.invalid"
	ourAPIKey  = "sk-conformance-ours"

	envOracleAxonhub     = "CONFORMANCE_ORACLE_AXONHUB"
	envOracleCLIProxyAPI = "CONFORMANCE_ORACLE_CLIPROXYAPI"
	envReportPath        = "CONFORMANCE_REPORT"

	oracleTimeout = 90 * time.Second
)

// sample 是 conformance/samples 下的一份对拍样本。
type sample struct {
	ID       string          `json:"id"`
	Class    string          `json:"class"`
	Side     string          `json:"side"`
	Phase    int             `json:"phase"`
	Inbound  string          `json:"inbound"`
	Outbound string          `json:"outbound"`
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Source   json.RawMessage `json:"source"`
	Request  json.RawMessage `json:"request"`
	Note     string          `json:"note"`
}

// outcome 统一表示「我方 / 某个 oracle」对同一请求的转换结果。
type outcome struct {
	Kind   string          `json:"kind"` // ok | error | unsupported | not_provided
	Error  string          `json:"error,omitempty"`
	Method string          `json:"method,omitempty"`
	Path   string          `json:"path,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type oracleSpec struct {
	Name string
	Env  string
}

var oracleSpecs = []oracleSpec{
	{Name: "axonhub", Env: envOracleAxonhub},
	{Name: "cliproxyapi", Env: envOracleCLIProxyAPI},
}

// repoRoot 从 internal/conformance 工作目录向上找到仓库根（含 conformance/samples）。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if st, err := os.Stat(filepath.Join(dir, "conformance", "samples")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("找不到仓库根（应包含 conformance/samples 目录）")
	return ""
}

func loadSamples(t *testing.T, dir string) []sample {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取样本目录 %s: %v", dir, err)
	}
	var samples []sample
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("读取样本 %s: %v", e.Name(), err)
		}
		var s sample
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("解析样本 %s: %v", e.Name(), err)
		}
		if err := validateSample(s, e.Name()); err != nil {
			t.Fatalf("样本 %s 不合法: %v", e.Name(), err)
		}
		samples = append(samples, s)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ID < samples[j].ID })
	return samples
}

func validateSample(s sample, file string) error {
	if s.ID == "" {
		return fmt.Errorf("缺少 id")
	}
	if file != s.ID+".json" {
		return fmt.Errorf("文件名应与 id 一致（%s.json）", s.ID)
	}
	if s.Class == "" {
		return fmt.Errorf("缺少 class")
	}
	switch s.Side {
	case "request":
		if s.Inbound == "" || s.Outbound == "" || s.Model == "" {
			return fmt.Errorf("request 样本需要 inbound/outbound/model")
		}
		if len(s.Request) == 0 {
			return fmt.Errorf("request 样本需要 request 字段")
		}
	case "response":
		if s.Phase < 2 {
			return fmt.Errorf("response 样本应标注 phase >= 2")
		}
	default:
		return fmt.Errorf("未知 side %q（应为 request 或 response）", s.Side)
	}
	return nil
}

func inboundTypeByName(name string) (inbound.InboundType, bool) {
	switch name {
	case "openai_chat":
		return inbound.InboundTypeOpenAIChat, true
	case "openai_responses":
		return inbound.InboundTypeOpenAIResponse, true
	case "anthropic":
		return inbound.InboundTypeAnthropic, true
	case "gemini":
		return inbound.InboundTypeGemini, true
	default:
		return 0, false
	}
}

func outboundTypeByName(name string) (outbound.OutboundType, bool) {
	switch name {
	case "chat":
		return outbound.OutboundTypeOpenAIChat, true
	case "responses":
		return outbound.OutboundTypeOpenAIResponse, true
	case "anthropic":
		return outbound.OutboundTypeAnthropic, true
	case "gemini":
		return outbound.OutboundTypeGemini, true
	default:
		return 0, false
	}
}

// runOurs 走与生产 parseRequest + 出站适配器一致的转换路径：
// 入站适配器把原始请求体解析成 InternalLLMRequest（含 Validate），
// 出站适配器再把它序列化成上游请求体。不做渠道选择、参数覆盖与改写。
func runOurs(ctx context.Context, s sample) outcome {
	inType, ok := inboundTypeByName(s.Inbound)
	if !ok {
		return outcome{Kind: "error", Error: fmt.Sprintf("未知 inbound %q", s.Inbound)}
	}
	inAdapter := inbound.Get(inType)
	if inAdapter == nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("inbound %q 无适配器", s.Inbound)}
	}
	internalReq, err := inAdapter.TransformRequest(ctx, s.Request)
	if err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("inbound %q: %v", s.Inbound, err)}
	}
	if err := internalReq.Validate(); err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("validate: %v", err)}
	}

	outType, ok := outboundTypeByName(s.Outbound)
	if !ok {
		return outcome{Kind: "error", Error: fmt.Sprintf("未知 outbound %q", s.Outbound)}
	}
	outAdapter := outbound.Get(outType)
	if outAdapter == nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("outbound %q 无适配器", s.Outbound)}
	}
	httpReq, err := outAdapter.TransformRequest(ctx, internalReq, ourBaseURL, ourAPIKey)
	if err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("outbound %q: %v", s.Outbound, err)}
	}
	if httpReq == nil {
		return outcome{Kind: "error", Error: "outbound 返回 nil 请求"}
	}
	body, err := io.ReadAll(httpReq.Body)
	if err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("读取我方请求体: %v", err)}
	}
	if !json.Valid(body) {
		return outcome{Kind: "error", Error: "我方请求体不是合法 JSON"}
	}
	path := ""
	if httpReq.URL != nil {
		path = httpReq.URL.Path
	}
	return outcome{Kind: "ok", Method: httpReq.Method, Path: path, Body: body}
}

type oracleResult struct {
	OK          bool            `json:"ok"`
	Unsupported bool            `json:"unsupported"`
	Error       string          `json:"error"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Body        json.RawMessage `json:"body"`
}

// runOracle 以子进程方式调用 oracle（stdin 一行 JSON -> stdout 一行 JSON）。
func runOracle(ctx context.Context, bin string, s sample) outcome {
	envelope := map[string]any{
		"from":    s.Inbound,
		"to":      s.Outbound,
		"model":   s.Model,
		"stream":  s.Stream,
		"request": json.RawMessage(s.Request),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("构造 oracle 输入: %v", err)}
	}

	runCtx, cancel := context.WithTimeout(ctx, oracleTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("oracle 执行失败: %v; stderr=%s", err, truncateText(stderr.String(), 400))}
	}
	var res oracleResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return outcome{Kind: "error", Error: fmt.Sprintf("解析 oracle 输出: %v; stdout=%s", err, truncateText(stdout.String(), 400))}
	}
	if res.Unsupported {
		return outcome{Kind: "unsupported"}
	}
	if !res.OK {
		return outcome{Kind: "error", Error: res.Error}
	}
	if len(res.Body) == 0 || !json.Valid(res.Body) {
		return outcome{Kind: "error", Error: "oracle 返回空或非法 JSON 请求体"}
	}
	return outcome{Kind: "ok", Method: res.Method, Path: res.Path, Body: res.Body}
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

// ----- 报告 -----

type conformanceReport struct {
	GeneratedAt string         `json:"generated_at"`
	Samples     []reportSample `json:"samples"`
}

type reportSample struct {
	ID       string                  `json:"id"`
	Class    string                  `json:"class"`
	Inbound  string                  `json:"inbound"`
	Outbound string                  `json:"outbound"`
	Side     string                  `json:"side,omitempty"`
	Note     string                  `json:"note,omitempty"`
	Ours     *outcome                `json:"ours,omitempty"`
	Oracles  map[string]reportOracle `json:"oracles,omitempty"`
}

type reportOracle struct {
	Outcome      outcome     `json:"outcome"`
	Diff         []diffEntry `json:"diff,omitempty"`
	EndpointDiff bool        `json:"endpoint_diff,omitempty"`
	Registry     string      `json:"registry,omitempty"` // clean | registered | pending | stale | missing | mismatch | not_provided
}

// writeReport 把完整对照报告写到 CONFORMANCE_REPORT 指定的路径（CI 里作为
// artifact 上传，用于偏差登记簿的首次校准）。未设置环境变量时只记日志。
func writeReport(t *testing.T, report *conformanceReport) {
	t.Helper()
	path := os.Getenv(envReportPath)
	if path == "" {
		t.Logf("CONFORMANCE_REPORT 未设置，跳过对照报告写入")
		return
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Errorf("序列化对照报告: %v", err)
		return
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Errorf("写入对照报告 %s: %v", path, err)
		return
	}
	t.Logf("对照报告已写入 %s（%d 个样本）", path, len(report.Samples))
}
