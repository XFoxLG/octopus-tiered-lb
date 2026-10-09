package airoute

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAIRouteChatCompletionRequestBody_JSONModeOn(t *testing.T) {
	body, err := buildAIRouteChatCompletionRequestBody(
		aiRouteService{Name: "svc", Model: "gpt-4o"},
		"system prompt", "user prompt", true)
	if err != nil {
		t.Fatalf("buildAIRouteChatCompletionRequestBody error = %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	var responseFormat map[string]any
	if err := json.Unmarshal(decoded["response_format"], &responseFormat); err != nil {
		t.Fatalf("response_format decode failed: %v", err)
	}
	if responseFormat["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want json_object", responseFormat["type"])
	}

	if string(decoded["temperature"]) != "0" {
		t.Fatalf("temperature = %s, want 0", string(decoded["temperature"]))
	}
}

func TestBuildAIRouteChatCompletionRequestBody_JSONModeOff(t *testing.T) {
	body, err := buildAIRouteChatCompletionRequestBody(
		aiRouteService{Name: "svc", Model: "gpt-4o"},
		"system prompt", "user prompt", false)
	if err != nil {
		t.Fatalf("buildAIRouteChatCompletionRequestBody error = %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	if _, exists := decoded["response_format"]; exists {
		t.Fatal("response_format should be omitted when jsonMode is false")
	}
}

func TestCallErrContainsResponseFormat(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"mentions field", `{"error":{"message":"response_format is not supported"}}`, true},
		{"case insensitive", `{"error":{"message":"Response_Format unsupported"}}`, true},
		{"other error", `{"error":{"message":"invalid api key"}}`, false},
		{"empty body", "", false},
	}
	for _, tc := range cases {
		if got := callErrContainsResponseFormat([]byte(tc.body)); got != tc.want {
			t.Fatalf("callErrContainsResponseFormat(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestBuildAIRouteSystemPrompt_ContainsFewShotExample(t *testing.T) {
	prompt := buildAIRouteSystemPrompt("")
	if !strings.Contains(prompt, "示例：") {
		t.Fatal("system prompt should contain the few-shot example marker")
	}
	if !strings.Contains(prompt, `"requested_model":"gpt-4o"`) {
		t.Fatal("system prompt should contain the few-shot example route JSON")
	}
}
