package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNonChatUsageEvidenceMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, body, basis, complete string
		in, out, total              *int64
		issues                      int
	}{
		{"emrank", `{"usage":{"prompt_tokens":451,"total_tokens":451}}`, "processing", "complete", ncp(451), nil, ncp(451), 0},
		{"total_only", `{"usage":{"total_tokens":1000}}`, "processing", "complete", nil, nil, ncp(1000), 0},
		{"gap", `{"usage":{"input_tokens":700,"total_tokens":1000}}`, "processing", "complete", ncp(700), nil, ncp(1000), 0},
		{"zero", `{"usage":{"input_tokens":100,"output_tokens":0}}`, "processing", "complete", ncp(100), ncp(0), ncp(100), 0},
		{"output_only", `{"tokens":{"output_tokens":2}}`, "processing", "partial", nil, ncp(2), ncp(2), 0},
		{"cohere", `{"meta":{"tokens":{"input_tokens":100,"output_tokens":2},"billed_units":{"input_tokens":90,"output_tokens":1,"search_units":1}}}`, "processing", "complete", ncp(100), ncp(2), ncp(102), 0},
		{"billed", `{"meta":{"billed_units":{"input_tokens":90}}}`, "billed", "partial", ncp(90), nil, ncp(90), 0},
		{"not_mix", `{"usage":{"input_tokens":100},"meta":{"billed_units":{"output_tokens":2}}}`, "processing", "partial", ncp(100), nil, ncp(100), 0},
		{"null", `{"usage":{"input_tokens":null},"meta":{"billed_units":{"input_tokens":90}}}`, "billed", "partial", ncp(90), nil, ncp(90), 0},
		{"decimal", `{"tokens":{"input_tokens":12.0,"output_tokens":2e1}}`, "processing", "complete", ncp(12), ncp(20), ncp(32), 0},
		{"conflict", `{"usage":{"prompt_tokens":2,"input_tokens":3}}`, "processing", "invalid", nil, nil, nil, 1},
		{"bad_no_billed_fallback", `{"usage":{"input_tokens":-1},"meta":{"billed_units":{"input_tokens":90}}}`, "processing", "invalid", nil, nil, nil, 1},
		{"fraction", `{"usage":{"input_tokens":1.5}}`, "processing", "invalid", nil, nil, nil, 1},
		{"overflow", `{"usage":{"input_tokens":9223372036854775808}}`, "processing", "invalid", nil, nil, nil, 1},
		{"sum_overflow", `{"usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`, "processing", "invalid", nil, nil, nil, 1},
		{"less_total", `{"usage":{"input_tokens":5,"total_tokens":4}}`, "processing", "invalid", nil, nil, nil, 1},
		{"search_only", `{"meta":{"billed_units":{"search_units":2}}}`, "missing", "missing", nil, nil, nil, 0},
		{"absent", `{}`, "missing", "missing", nil, nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := ObserveNonChatResponse(strings.NewReader(tc.body), "rerank")
			s := o.Summary
			if s.Basis != tc.basis || s.Completeness != tc.complete || !ncEqual(s.InputTokens, tc.in) || !ncEqual(s.OutputTokens, tc.out) || !ncEqual(s.TotalTokens, tc.total) || len(o.Issues) != tc.issues {
				t.Fatalf("observation=%+v summary=%+v", o, s)
			}
			if tc.issues > 0 && o.Issues[0].Path == "" {
				t.Fatal("missing issue path")
			}
		})
	}
}
func ncp(v int64) *int64       { return &v }
func ncEqual(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func TestNonChatResponseLargeAndInvalid(t *testing.T) {
	for _, tail := range []bool{false, true} {
		usage := `"meta":{"tokens":{"input_tokens":20,"output_tokens":0}},`
		result := `"results":[{"index":0,"relevance_score":0.9,"document":{"text":"` + strings.Repeat("x", 2<<20) + `"}}]`
		body := "{" + usage + result + "}"
		if tail {
			body = "{" + result + "," + strings.TrimSuffix(usage, ",") + "}"
		}
		var forwarded bytes.Buffer
		o := ObserveNonChatResponse(&smallTeeReader{reader: strings.NewReader(body), out: &forwarded}, "rerank")
		if !ncEqual(o.Summary.TotalTokens, ncp(20)) || !ncEqual(o.Summary.Counts.RerankResults, ncp(1)) || forwarded.String() != body {
			t.Fatalf("large response lost evidence: %+v", o)
		}
	}
	for _, body := range []string{`{"results":[`, `{"usage":{"input_tokens":20}} trailing`, `{"results":[{},]}`, `{"usage":{"total_tokens":2},"results":[{"document":"broken}`} {
		o := ObserveNonChatResponse(strings.NewReader(body), "rerank")
		if o.ResponseState != "invalid_json" || o.Summary.TotalTokens != nil || o.Summary.Counts.RerankResults != nil {
			t.Fatalf("accepted invalid JSON: %+v", o)
		}
	}
}

type smallTeeReader struct {
	reader *strings.Reader
	out    *bytes.Buffer
}

func (r *smallTeeReader) Read(p []byte) (int, error) {
	if len(p) > 31 {
		p = p[:31]
	}
	n, e := r.reader.Read(p)
	r.out.Write(p[:n])
	return n, e
}

func TestNonChatCounts(t *testing.T) {
	for _, tc := range []struct {
		body  string
		count int64
	}{{`{"input":"text"}`, 1}, {`{"input":["a","b"]}`, 2}, {`{"input":[10,20,30]}`, 1}, {`{"input":[[10,20],[30]]}`, 2}} {
		counts := ObserveNonChatRequest([]byte(tc.body), "embeddings")
		if !ncEqual(counts.EmbeddingInputItems, ncp(tc.count)) {
			t.Fatalf("%s: %+v", tc.body, counts)
		}
		var req struct {
			Input EmbeddingInput `json:"input"`
		}
		if e := json.Unmarshal([]byte(tc.body), &req); e != nil {
			t.Fatal(e)
		}
		clone := cloneEmbeddingInput(&req.Input)
		b, e := json.Marshal(clone)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Contains([]byte(tc.body), b) {
			t.Fatalf("input changed: %s", b)
		}
	}
	for _, tc := range []struct {
		body string
		dim  *int64
	}{{`{"data":[{"embedding":[1,2]},{"embedding":[3,4]}]}`, ncp(2)}, {`{"data":[{"embedding":"abcd"},{"embedding":"abcd"}]}`, nil}} {
		o := ObserveNonChatResponse(strings.NewReader(tc.body), "embeddings")
		if !ncEqual(o.Summary.Counts.EmbeddingVectors, ncp(2)) || !ncEqual(o.Summary.Counts.EmbeddingDimensions, tc.dim) {
			t.Fatal(o.Summary.Counts)
		}
	}
	o := ObserveNonChatResponse(strings.NewReader(`{"results":[]}`), "rerank")
	if !ncEqual(o.Summary.Counts.RerankResults, ncp(0)) {
		t.Fatal("lost explicit zero results")
	}
}
