package helper

import (
	"context"
	"encoding/json"
	"testing"

	ai "github.com/lingyuins/octopus/internal/transformer/inbound/anthropic"
	oi "github.com/lingyuins/octopus/internal/transformer/inbound/openai"
	ao "github.com/lingyuins/octopus/internal/transformer/outbound/anthropic"
	oo "github.com/lingyuins/octopus/internal/transformer/outbound/openai"
)

func TestStructuredConstraintsSurviveProtocolRoundTrips(t *testing.T) {
	t.Run("Responses", func(t *testing.T) {
		in := &oi.ResponseInbound{}
		req, err := in.TransformRequest(context.Background(), []byte(`{"model":"test","input":"hi","text":{"format":{"type":"json_schema","name":"verify_schema","strict":false,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := (&oo.ResponseOutbound{}).TransformRequest(context.Background(), req, "https://mock.example.test/v1", "")
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err = json.NewDecoder(wire.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		wire.Body.Close()
		text, _ := body["text"].(map[string]any)
		format, _ := text["format"].(map[string]any)
		if format["name"] != "verify_schema" || format["strict"] != false || format["schema"] == nil {
			t.Fatalf("constraint dropped: %#v", format)
		}
	})
	t.Run("Messages", func(t *testing.T) {
		in := &ai.MessagesInbound{}
		req, err := in.TransformRequest(context.Background(), []byte(`{"model":"test","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"verify_tool","input_schema":{"type":"object","properties":{}}}],"tool_choice":{"type":"tool","name":"verify_tool","disable_parallel_tool_use":true},"output_config":{"effort":"low","format":{"type":"json_schema","schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`))
		if err != nil {
			t.Fatal(err)
		}
		wire, err := (&ao.MessageOutbound{}).TransformRequest(context.Background(), req, "https://mock.example.test/v1", "")
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err = json.NewDecoder(wire.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		wire.Body.Close()
		config, _ := body["output_config"].(map[string]any)
		format, _ := config["format"].(map[string]any)
		if format["schema"] == nil {
			t.Fatalf("constraint dropped: %#v", config)
		}
		choice, _ := body["tool_choice"].(map[string]any)
		if choice["type"] != "tool" || choice["name"] != "verify_tool" || choice["disable_parallel_tool_use"] != true {
			t.Fatalf("tool choice dropped: %#v", choice)
		}
	})
}
