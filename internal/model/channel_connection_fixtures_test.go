package model

import (
	"context"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMiMoConnectionCompatibilityFixtures(t *testing.T) {
	for _, profile := range []string{"mimo", "legacy_mimo"} {
		t.Run(profile, func(t *testing.T) {
			e := connectionFixture("chat")
			e.Compatibility = profile
			e.URL = "https://relay.example/v1"
			p := ConnectionPlan{Endpoint: &e, AdapterType: e.AdapterType()}
			a := p.Adapter()
			r := connectionRequest(tm.APIFormatOpenAIChatCompletion)
			stream := true
			r.Stream = &stream
			request, err := (&Channel{}).BuildConnectionRequest(context.Background(), p, a, r, "fixture-key")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"role":"system"`) || !strings.Contains(string(body), `"include_usage":true`) || !strings.Contains(string(body), `"content":"test"`) {
				t.Fatalf("MiMo compatibility lost: %s", body)
			}
			fixture := `{"id":"mimo","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":"reasoning fixture","tool_calls":[{"id":"tool-1","type":"function","function":{"name":"verify","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
			decoded, err := a.TransformResponse(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture)), Header: http.Header{"Content-Type": []string{"application/json"}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded.Choices) != 1 || decoded.Choices[0].Message == nil || len(decoded.Choices[0].Message.ToolCalls) != 1 || decoded.Choices[0].Message.ReasoningContent == nil {
				t.Fatalf("MiMo tool/reasoning response lost %+v", decoded)
			}
			chunk, err := a.TransformStream(context.Background(), []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`))
			if err != nil || len(chunk.Choices) != 1 || chunk.Choices[0].Delta.ReasoningContent == nil {
				t.Fatal("MiMo reasoning delta lost")
			}
		})
	}
}

func TestCloudflareCompatibilityRetainsNativeAndChatShapes(t *testing.T) {
	e := connectionFixture("cloudflare")
	p := ConnectionPlan{Endpoint: &e, AdapterType: e.AdapterType()}
	a := p.Adapter()
	decoded, err := a.TransformResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"success":true,"errors":[],"result":{"response":"native reply"}}`))})
	if err != nil || len(decoded.Choices) != 1 || *decoded.Choices[0].Message.Content.Content != "native reply" {
		t.Fatalf("native fixture failed %v", err)
	}
	chunk, err := a.TransformStream(context.Background(), []byte(`{"response":"native delta"}`))
	if err != nil || *chunk.Choices[0].Delta.Content.Content != "native delta" {
		t.Fatalf("native stream fixture failed %v", err)
	}
	chat := connectionFixture("chat")
	chat.URL = "https://worker.example/v1"
	url, err := chat.BuildURL("m", false)
	if err != nil || url != "https://worker.example/v1/chat/completions" {
		t.Fatal("worker hosting was mistaken for native protocol")
	}
}
