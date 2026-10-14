// Command oracle-cliproxyapi is a test-only conformance oracle.
//
// It exposes CLIProxyAPI's public SDK translation entry points
// (sdk/translator + sdk/translator/builtin) as a small stdin/stdout CLI so the
// Octopus conformance harness can compare the same request transformation
// across three independent implementations:
//
//	octopus (our engine)  vs  axonhub (primary oracle)  vs  cliproxyapi (secondary oracle)
//
// Upstream project: https://github.com/router-for-me/CLIProxyAPI (MIT).
// Pinned in go.mod; see ../../README.md for the licensing note.
//
// Protocol (one JSON object on stdin -> one JSON object on stdout):
//
//	{"from":"anthropic","to":"chat","model":"...","stream":false,"request":{...}}
//
//	{"ok":true,"body":{...}}                            translated request body
//	{"ok":false,"unsupported":true}                     no registered transformer
//	{"ok":false,"error":"..."}                          translation rejected/failed
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	// The builtin package is the official SDK registration entry point
	// ("exposes the built-in translator registrations for SDK users").
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	_ "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator/builtin"
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
	Body        json.RawMessage `json:"body,omitempty"`
}

// mapFormat 把本仓库对拍词表（我方 inbound/outbound 命名）映射到
// CLIProxyAPI SDK 的 Format。CLIProxyAPI 在请求方向上的语义与我方一致：
// from = 客户端（入站）协议，to = 上游（出站）协议。
func mapFormat(name string) (sdktranslator.Format, bool) {
	switch name {
	case "chat", "openai_chat":
		return sdktranslator.FormatOpenAI, true
	case "responses", "openai_responses":
		return sdktranslator.FormatOpenAIResponse, true
	case "anthropic":
		return sdktranslator.FormatClaude, true
	case "gemini":
		return sdktranslator.FormatGemini, true
	default:
		return "", false
	}
}

func run(in oracleInput) oracleOutput {
	from, okFrom := mapFormat(in.From)
	to, okTo := mapFormat(in.To)
	if !okFrom || !okTo {
		return oracleOutput{OK: false, Unsupported: true}
	}
	if !sdktranslator.HasRequestTransformerByFormatName(from, to) {
		return oracleOutput{OK: false, Unsupported: true}
	}

	translated := sdktranslator.TranslateRequestByFormatName(from, to, in.Model, in.Request, in.Stream)
	if len(translated) == 0 {
		return oracleOutput{OK: false, Error: fmt.Sprintf("empty translation %s -> %s", in.From, in.To)}
	}
	if !json.Valid(translated) {
		return oracleOutput{OK: false, Error: fmt.Sprintf("translation %s -> %s returned invalid JSON", in.From, in.To)}
	}
	return oracleOutput{OK: true, Body: json.RawMessage(translated)}
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

// runPanicSafe 把上游库的 panic 也折叠成 error 结果，保证对拍进程永远
// 输出一行合法 JSON，而不是让整个 harness 因为子进程崩溃而失去信息。
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
