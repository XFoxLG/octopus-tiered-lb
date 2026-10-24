package helper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestProbeRejectsMetadataAndUnrelatedTools(t *testing.T) {
	cases := []struct {
		name, item, body string
		want             m.ProbeVerdict
	}{
		{"gemini-thought-is-not-answer", m.ProbeItemStructuredOutput, `{"candidates":[{"content":{"parts":[{"thought":true,"text":"{\"ok\":true}"},{"text":"wrong answer"}]}}]}`, m.ProbeVerdictFail},
		{"metadata-ok", m.ProbeItemStructuredOutput, `{"metadata":{"ok":true},"choices":[{"message":{"content":"plain text"}}]}`, m.ProbeVerdictFail},
		{"metadata-text", m.ProbeItemStructuredOutput, `{"metadata":{"text":"{\"ok\":true}"},"choices":[{"message":{"content":"plain text"}}]}`, m.ProbeVerdictFail},
		{"extra-property", m.ProbeItemStructuredOutput, `{"choices":[{"message":{"content":"{\"ok\":true,\"extra\":1}"}}]}`, m.ProbeVerdictFail},
		{"fenced-json", m.ProbeItemStructuredOutput, `{"candidates":[{"content":{"parts":[{"text":"\u0060\u0060\u0060json\n{\"ok\":true}\n\u0060\u0060\u0060"}]}}]}`, m.ProbeVerdictFail},
		{"unrelated-tool", m.ProbeItemToolCalling, `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"another_tool","arguments":"{}"}}]}}]}`, m.ProbeVerdictFail},
		{"metadata-tool", m.ProbeItemToolCalling, `{"metadata":{"tool_calls":[{"function":{"name":"verify_tool","arguments":"{}"}}]},"choices":[{"message":{"content":"OK"}}]}`, m.ProbeVerdictFail},
		{"refusal-is-inconclusive", m.ProbeItemToolCalling, `{"choices":[{"message":{"refusal":"Cannot fulfill this request."}}]}`, m.ProbeVerdictUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &probeRequestOutcome{StatusCode: 200, Verdict: m.ProbeVerdictPass, Summary: "200 OK", Body: tc.body}
			if got := judgeCapabilityResponse(tc.item, outcome); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if outcome.Summary == "200 OK" {
				t.Error("HTTP status alone does not explain the capability verdict")
			}
		})
	}
}

func TestProbeDetailPreservesWhitespaceAndBoundedUTF8(t *testing.T) {
	secret := "sk-local-probe-secret-0123456789"
	body := "{\n  \"text\": \"" + strings.Repeat("测试", 300) + "\",\n  \"authorization\": \"Bearer " + secret + "\"\n}"
	got := redactProbeDetail(body, secret)
	if strings.Contains(got, secret) {
		t.Fatal("secret leaked")
	}
	if !strings.Contains(got, "\n  ") || !strings.Contains(got, strings.Repeat("测试", 300)) {
		t.Fatal("the small response was collapsed or truncated")
	}
	large := redactProbeDetail(strings.Repeat("测", capabilityProbeMaxDetail))
	if len(large) > capabilityProbeMaxDetail {
		t.Fatal("saved detail exceeded its byte bound")
	}
	if !utf8.ValidString(large) {
		t.Fatal("truncation split a UTF-8 rune")
	}
}

func TestConfiguredProbeSendsNativeConstraintsAndKeepsInterfacesSeparate(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	seen := map[string][]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		seen[r.URL.Path] = append(seen[r.URL.Path], body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}","tool_calls":[{"type":"function","function":{"name":"verify_tool","arguments":"{}"}}]},"finish_reason":"stop"}]}`))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]},{"type":"function_call","name":"verify_tool","arguments":"{}","call_id":"call-1"}]}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"{\"ok\":true}"},{"type":"tool_use","id":"t","name":"verify_tool","input":{}}],"stop_reason":"end_turn"}`))
		default:
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"ok\":true}"},{"functionCall":{"name":"verify_tool","args":{}}}]},"finishReason":"STOP"}]}`))
		}
	}))
	defer srv.Close()
	cfg := &m.ConnectionConfig{Version: 1, Selection: "configured", Catalog: m.ModelCatalog{Format: "manual"}}
	for _, protocol := range []string{"chat", "responses", "messages", "gemini"} {
		cfg.Endpoints = append(cfg.Endpoints, m.ChannelEndpoint{ID: protocol + "-id", Protocol: protocol, URL: srv.URL, URLMode: "base", Auth: "default"})
	}
	ch := &m.Channel{Type: outbound.OutboundTypeOpenAIChat, ConnectionConfig: cfg}
	run, err := RunChannelCapabilityProbe(context.Background(), ch, m.ChannelProbeRequest{ModelName: "test-model", KeyIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range run.Results {
		if result.Protocol != strings.TrimSuffix(result.EndpointID, "-id") {
			t.Errorf("actual protocol missing or wrong: %+v", result)
		}
		if result.EndpointID == "" {
			t.Errorf("result %s lost interface identity", result.Item)
		}
		if result.Item == m.ProbeItemStructuredOutput && result.Verdict != m.ProbeVerdictPass {
			t.Errorf("%s structured: %s (%s)", result.EndpointID, result.Verdict, result.Summary)
		}
		if result.Item == m.ProbeItemWebSearch && (result.EndpointID == "chat-id" || result.EndpointID == "messages-id") && result.Verdict != m.ProbeVerdictUnknown {
			t.Errorf("an unimplemented native probe must be inconclusive: %+v", result)
		}
	}
	if len(CapabilityVerdicts(run)) != 0 {
		t.Error("per-interface evidence must not overwrite channel-wide capability flags")
	}
	find := func(suffix, key string) map[string]any {
		for p, bodies := range seen {
			if strings.HasSuffix(p, suffix) {
				for _, b := range bodies {
					if _, ok := b[key]; ok {
						if key == "generationConfig" {
							encoded, _ := json.Marshal(b[key])
							if !strings.Contains(string(encoded), "responseSchema") {
								continue
							}
						}
						return b
					}
				}
			}
		}
		t.Errorf("no %s request carrying %s: %v", suffix, key, seen)
		return nil
	}
	chat := find("/chat/completions", "response_format")
	responses := find("/responses", "text")
	messages := find("/messages", "output_config")
	gemini := find(":generateContent", "generationConfig")
	if chat != nil {
		b, _ := json.Marshal(chat["response_format"])
		if !strings.Contains(string(b), "verify_schema") || !strings.Contains(string(b), "\"strict\":true") || !strings.Contains(string(b), "\"schema\":") {
			t.Error("Chat schema name lost")
		}
	}
	if responses != nil {
		b, _ := json.Marshal(responses["text"])
		for _, part := range []string{"verify_schema", "\"schema\":", "\"strict\":true"} {
			if !strings.Contains(string(b), part) {
				t.Errorf("Responses constraint lost %s: %s", part, b)
			}
		}
	}
	if messages != nil {
		b, _ := json.Marshal(messages["output_config"])
		if !strings.Contains(string(b), "\"schema\":") || !strings.Contains(string(b), "\"format\":") {
			t.Errorf("Messages schema lost: %s", b)
		}
	}
	if gemini != nil {
		b, _ := json.Marshal(gemini["generationConfig"])
		if !strings.Contains(string(b), "responseSchema") || !strings.Contains(string(b), "application/json") {
			t.Errorf("Gemini constraint lost: %s", b)
		}
	}
	nativeTool := find("/messages", "tool_choice")
	if nativeTool != nil {
		b, _ := json.Marshal(nativeTool["tool_choice"])
		if !strings.Contains(string(b), "\"type\":\"any\"") {
			t.Errorf("Messages did not force tool choice: %s", b)
		}
	}
}

func TestProbeFailureKeepsUpstreamErrorInExplanation(t *testing.T) {
	outcome := &probeRequestOutcome{StatusCode: 400, Verdict: m.ProbeVerdictFail, Summary: "400 Bad Request", Body: `{"error":{"message":"Invalid tool type: "}}`}
	if got := judgeCapabilityResponse(m.ProbeItemToolCalling, outcome); got != m.ProbeVerdictFail {
		t.Fatal(got)
	}
	if !strings.Contains(outcome.Summary, "Invalid tool type") {
		t.Fatal("upstream explanation missing")
	}
}

func TestProbeLongErrorRedactsBeforeSummaryTruncation(t *testing.T) {
	secret := "custom-key-near-summary-boundary-0123456789"
	outcome := &probeRequestOutcome{StatusCode: 400, Verdict: m.ProbeVerdictFail, Summary: "400 Bad Request", Body: strings.Repeat("x", 480) + secret}
	judgeCapabilityResponse(m.ProbeItemToolCalling, outcome)
	result := buildProbeResult(&m.ChannelProbeRun{}, m.ProbeKindCapability, m.ProbeItemToolCalling, outcome, secret)
	summary := truncateProbeSummary(result.Summary)
	if strings.Contains(summary, "custom-key") {
		t.Fatal("a partial credential survived truncation")
	}
}
