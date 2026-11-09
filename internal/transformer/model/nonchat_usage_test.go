package model

import "testing"

func TestParseNonChatTokenUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		input, output  int64
		reportedOutput bool
	}{
		{"embedding", `{"usage":{"prompt_tokens":451,"total_tokens":451}}`, 451, 0, false},
		{"rerank", `{"usage":{"input_tokens":35178}}`, 35178, 0, false},
		{"explicit_zero", `{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, 0, 0, true},
		{"siliconflow", `{"tokens":{"input_tokens":245,"output_tokens":3}}`, 245, 3, true},
		{"matching_envelopes", `{"usage":{"prompt_tokens":245,"total_tokens":248},"tokens":{"input_tokens":245,"output_tokens":3}}`, 245, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := ParseNonChatTokenUsage([]byte(tc.body))
			if u == nil || u.PromptTokens != tc.input || u.CompletionTokens != tc.output || u.TotalTokens != tc.input+tc.output || u.NonChatOutputReported == nil || *u.NonChatOutputReported != tc.reportedOutput {
				t.Fatalf("usage=%#v", u)
			}
		})
	}
	for _, body := range []string{
		`{}`, `{"usage":null}`, `{"usage":{}}`, `{"usage":{"total_tokens":245}}`,
		`{"meta":{"billed_units":{"search_units":2}}}`, `{"results":[{"index":0,"relevance_score":1}]}`,
		`{"tokens":{"input_tokens":1.5}}`, `{"tokens":{"input_tokens":-1}}`,
		`{"usage":{"input_tokens":2,"output_tokens":-1}}`,
		`{"usage":{"prompt_tokens":2,"input_tokens":3}}`,
		`{"usage":{"input_tokens":2},"tokens":{"input_tokens":3}}`,
		`{"usage":{"input_tokens":2,"total_tokens":3}}`,
		`{"error":{},"tokens":{"input_tokens":2}}`,
		`{"tokens":{"input_tokens":9223372036854775807,"output_tokens":1}}`,
	} {
		if got := ParseNonChatTokenUsage([]byte(body)); got != nil {
			t.Errorf("invented usage for %s: %#v", body, got)
		}
	}
}
