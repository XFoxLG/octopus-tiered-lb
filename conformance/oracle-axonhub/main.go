// Command oracle-axonhub is a test-only conformance oracle.
//
// It runs a request through AxonHub's llm transformer stack (inbound parse ->
// unified llm.Request -> outbound serialize) and prints the resulting upstream
// request body, so the Octopus conformance harness can diff three independent
// implementations of the same protocol conversion:
//
//	octopus (our engine)  vs  axonhub (primary oracle)  vs  cliproxyapi (secondary oracle)
//
// Upstream project: https://github.com/looplj/axonhub (Apache-2.0, module
// github.com/looplj/axonhub/llm). Pinned in go.mod; see ../../README.md.
//
// Protocol (one JSON object on stdin -> one JSON object on stdout):
//
//	{"from":"anthropic","to":"chat","model":"...","stream":false,"request":{...}}
//
//	{"ok":true,"method":"POST","path":"/v1/chat/completions","body":{...}}
//	{"ok":false,"unsupported":true}      unknown format name
//	{"ok":false,"error":"..."}           inbound/outbound transform failed
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	txanthropic "github.com/looplj/axonhub/llm/transformer/anthropic"
	txgemini "github.com/looplj/axonhub/llm/transformer/gemini"
	txopenai "github.com/looplj/axonhub/llm/transformer/openai"
	txresponses "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// 对拍统一使用的占位上游地址与凭据。两个 oracle 与我方 harness 使用同一组
// 占位值，保证差异只来自协议转换本身，而不是地址归一化规则。
const (
	upstreamBaseURL = "https://upstream.invalid"
	upstreamAPIKey  = "sk-conformance-oracle"
)

type oracleInput struct {
	From    string          `json:"from"`
	To      string          `json:"to"`
	Model   string          `json:"model"`
	Stream  bool            `json:"stream"`
	Request json.RawMessage `json:"request"`
}

type oracleOutput struct {
	OK          bool            `json:"ok"`
	Unsupported bool            `json:"unsupported,omitempty"`
	Error       string          `json:"error,omitempty"`
	Method      string          `json:"method,omitempty"`
	Path        string          `json:"path,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

// inboundPath 给出每种入站协议在真实网关里的请求路径，供需要从 URL 读取
// 信息的入站转换器（例如 Gemini 的 model 在路径上）使用。
func inboundPath(from, model string) string {
	switch from {
	case "openai_chat":
		return "/v1/chat/completions"
	case "openai_responses":
		return "/v1/responses"
	case "anthropic":
		return "/v1/messages"
	case "gemini":
		return "/v1beta/models/" + model + ":generateContent"
	default:
		return "/"
	}
}

func newInbound(from string) (transformer.Inbound, bool) {
	switch from {
	case "openai_chat":
		return txopenai.NewInboundTransformer(), true
	case "openai_responses":
		return txresponses.NewInboundTransformer(), true
	case "anthropic":
		return txanthropic.NewInboundTransformer(), true
	case "gemini":
		return txgemini.NewInboundTransformer(), true
	default:
		return nil, false
	}
}

func newOutbound(to string) (transformer.Outbound, bool, error) {
	switch to {
	case "chat":
		t, err := txopenai.NewOutboundTransformer(upstreamBaseURL, upstreamAPIKey)
		return t, true, err
	case "responses":
		t, err := txresponses.NewOutboundTransformer(upstreamBaseURL, upstreamAPIKey)
		if t == nil {
			return nil, true, err
		}
		return t, true, err
	case "anthropic":
		t, err := txanthropic.NewOutboundTransformer(upstreamBaseURL, upstreamAPIKey)
		return t, true, err
	case "gemini":
		t, err := txgemini.NewOutboundTransformer(upstreamBaseURL, upstreamAPIKey)
		return t, true, err
	default:
		return nil, false, nil
	}
}

func run(in oracleInput) oracleOutput {
	inbound, okIn := newInbound(in.From)
	if !okIn {
		return oracleOutput{OK: false, Unsupported: true}
	}
	outbound, okOut, err := newOutbound(in.To)
	if !okOut {
		return oracleOutput{OK: false, Unsupported: true}
	}
	if err != nil {
		return oracleOutput{OK: false, Error: fmt.Sprintf("build outbound %q: %v", in.To, err)}
	}

	ctx := context.Background()
	path := inboundPath(in.From, in.Model)
	httpReq := &httpclient.Request{
		Method:      http.MethodPost,
		URL:         "http://inbound.invalid" + path,
		Path:        path,
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		Body:        in.Request,
		RequestType: string(llm.RequestTypeChat),
	}

	llmReq, err := inbound.TransformRequest(ctx, httpReq)
	if err != nil {
		return oracleOutput{OK: false, Error: fmt.Sprintf("inbound %q: %v", in.From, err)}
	}
	provReq, err := outbound.TransformRequest(ctx, llmReq)
	if err != nil {
		return oracleOutput{OK: false, Error: fmt.Sprintf("outbound %q: %v", in.To, err)}
	}
	if provReq == nil {
		return oracleOutput{OK: false, Error: "outbound returned nil request"}
	}

	body := provReq.Body
	if len(body) == 0 && len(provReq.JSONBody) > 0 {
		body = provReq.JSONBody
	}
	if len(body) == 0 {
		return oracleOutput{OK: false, Error: "outbound returned empty body"}
	}
	if !json.Valid(body) {
		return oracleOutput{OK: false, Error: "outbound body is not valid JSON"}
	}

	return oracleOutput{
		OK:     true,
		Method: provReq.Method,
		Path:   requestPath(provReq),
		Body:   json.RawMessage(body),
	}
}

func requestPath(req *httpclient.Request) string {
	if req.URL != "" {
		if u, err := url.Parse(req.URL); err == nil && u.Path != "" {
			return u.Path
		}
	}
	return req.Path
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		emit(oracleOutput{OK: false, Error: fmt.Sprintf("read stdin: %v", err)})
		return
	}
	var in oracleInput
	if err := json.Unmarshal(raw, &in); err != nil {
		emit(oracleOutput{OK: false, Error: fmt.Sprintf("decode input: %v", err)})
		return
	}
	emit(runPanicSafe(in))
}

// runPanicSafe 把上游库的 panic 折叠成 error 结果，保证对拍进程永远输出
// 一行合法 JSON。
func runPanicSafe(in oracleInput) (out oracleOutput) {
	defer func() {
		if r := recover(); r != nil {
			out = oracleOutput{OK: false, Error: fmt.Sprintf("panic: %v", r)}
		}
	}()
	return run(in)
}

func emit(out oracleOutput) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
		os.Exit(1)
	}
}
