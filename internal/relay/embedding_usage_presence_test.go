package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	m "github.com/lingyuins/octopus/internal/model"
	in "github.com/lingyuins/octopus/internal/transformer/inbound/openai"
	out "github.com/lingyuins/octopus/internal/transformer/outbound/openai"
)

func TestEmbeddingUsagePresenceSurvivesConversionAndAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, usage, state string
		input, output      int64
		outputPresent      bool
	}{
		{"input_only", `{"prompt_tokens":451,"total_tokens":451}`, m.RelayLogUsageReportedInputOnly, 451, 0, false},
		{"explicit_zero", `{"prompt_tokens":451,"completion_tokens":0,"total_tokens":451}`, m.RelayLogUsageReportedFull, 451, 0, true},
		{"reported_output", `{"prompt_tokens":451,"completion_tokens":3,"total_tokens":454}`, m.RelayLogUsageReportedFull, 451, 3, true},
		{"all_zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, m.RelayLogUsageReportedFull, 0, 0, true},
		{"total_only", `{"total_tokens":1000}`, m.RelayLogUsageReported, 0, 0, false},
		{"missing", `null`, m.RelayLogUsageNotReported, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"object":"list","model":"embedding-fixture","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":` + tc.usage + `}`
			response, err := (&out.EmbeddingOutbound{}).TransformResponse(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})
			if err != nil {
				t.Fatal(err)
			}
			if got := resolveUsageState(response, nil); got != tc.state {
				t.Fatalf("state=%s want %s", got, tc.state)
			}
			metrics := &RelayMetrics{}
			metrics.SetInternalResponse(response, "embedding-fixture")
			if metrics.Stats.InputToken != tc.input || metrics.Stats.OutputToken != tc.output {
				t.Fatalf("stats=%#v", metrics.Stats)
			}
			raw, err := (&in.EmbeddingInbound{}).TransformResponse(context.Background(), response)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != body {
				t.Fatalf("response bytes changed: %s", raw)
			}
			if tc.name == "total_only" && metrics.Stats.TotalTokens() != 1000 {
				t.Fatalf("total-only usage lost: %+v", metrics.Stats)
			}
			var wire struct {
				Data []struct {
					Embedding []float64 `json:"embedding"`
				} `json:"data"`
				Usage map[string]json.RawMessage `json:"usage"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Data) != 1 || len(wire.Data[0].Embedding) != 2 || wire.Data[0].Embedding[1] != 0.2 {
				t.Fatalf("vectors changed: %s", raw)
			}
			_, present := wire.Usage["completion_tokens"]
			if present != tc.outputPresent {
				t.Fatalf("completion presence=%v want %v: %s", present, tc.outputPresent, raw)
			}
			if present {
				var count int64
				_ = json.Unmarshal(wire.Usage["completion_tokens"], &count)
				if count != tc.output {
					t.Fatalf("completion=%d", count)
				}
			}
			if tc.name == "missing" && wire.Usage != nil {
				t.Fatalf("invented usage: %s", raw)
			}
		})
	}
}
