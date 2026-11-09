package model

import (
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"

	jsoniter "github.com/json-iterator/go"
)

// NonChatCounters keeps absence distinct from a provider's explicit zero.
type NonChatCounters struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	TotalTokens  *int64 `json:"total_tokens,omitempty"`
}
type NonChatCounts struct {
	EmbeddingInputItems *int64 `json:"embedding_input_items,omitempty"`
	EmbeddingVectors    *int64 `json:"embedding_vectors,omitempty"`
	EmbeddingDimensions *int64 `json:"embedding_dimensions,omitempty"`
	RerankDocuments     *int64 `json:"rerank_documents,omitempty"`
	RerankTopN          *int64 `json:"rerank_top_n,omitempty"`
	RerankResults       *int64 `json:"rerank_results,omitempty"`
}
type NonChatUsageSource struct {
	Path  string `json:"path"`
	State string `json:"state"`
	Value *int64 `json:"value,omitempty"`
}
type NonChatUsageIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type NonChatUnits struct {
	SearchUnits *float64 `json:"search_units,omitempty"`
}
type NonChatUsageSummary struct {
	Basis        string `json:"basis"`
	Completeness string `json:"completeness"`
	NonChatCounters
	Counts      NonChatCounts `json:"counts"`
	FirstByteMS *int64        `json:"first_byte_ms,omitempty"`
	ResponseMS  *int64        `json:"response_ms,omitempty"`
}

// NonChatObservation contains metadata only: no documents, vectors or score lists.
type NonChatObservation struct {
	Processing        NonChatCounters      `json:"processing"`
	Billed            NonChatCounters      `json:"billed"`
	Units             NonChatUnits         `json:"units"`
	Sources           []NonChatUsageSource `json:"sources,omitempty"`
	Issues            []NonChatUsageIssue  `json:"issues,omitempty"`
	ResponseState     string               `json:"response_state"`
	Summary           NonChatUsageSummary  `json:"-"`
	processingInvalid bool
	billedInvalid     bool
	providerError     bool
}

func (o *NonChatObservation) issue(path, reason string) {
	if len(o.Issues) < 64 {
		o.Issues = append(o.Issues, NonChatUsageIssue{path, reason})
	}
}
func (o *NonChatObservation) source(path, state string, value *int64) {
	if len(o.Sources) < 128 {
		o.Sources = append(o.Sources, NonChatUsageSource{path, state, value})
	}
}

// exactNonNegativeInt accepts JSON integral decimals/exponents without float64
// rounding or an allocation proportional to an untrusted exponent.
func exactNonNegativeInt(raw string) (int64, string) {
	if !json.Valid([]byte(raw)) {
		return 0, "invalid_number"
	}
	if strings.HasPrefix(raw, "-") {
		return 0, "negative_count"
	}
	mantissa, exponent := raw, int64(0)
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		mantissa = raw[:i]
		var err error
		exponent, err = strconv.ParseInt(raw[i+1:], 10, 32)
		if err != nil {
			return 0, "count_out_of_range"
		}
	}
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		exponent -= int64(len(mantissa) - i - 1)
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return 0, ""
	}
	if exponent < 0 {
		if -exponent >= int64(len(mantissa)) {
			return 0, "non_integer_count"
		}
		cut := len(mantissa) + int(exponent)
		if strings.Trim(mantissa[cut:], "0") != "" {
			return 0, "non_integer_count"
		}
		mantissa, exponent = mantissa[:cut], 0
	}
	if int64(len(mantissa))+exponent > 19 {
		return 0, "count_out_of_range"
	}
	value, err := strconv.ParseInt(mantissa+strings.Repeat("0", int(exponent)), 10, 64)
	if err != nil {
		return 0, "count_out_of_range"
	}
	return value, ""
}

func (o *NonChatObservation) readCounter(it *jsoniter.Iterator, path string, target **int64, invalid *bool) {
	if it.WhatIsNext() == jsoniter.NilValue {
		it.ReadNil()
		o.source(path, "null", nil)
		return
	}
	if it.WhatIsNext() != jsoniter.NumberValue {
		it.Skip()
		*invalid = true
		o.source(path, "invalid", nil)
		o.issue(path, "expected_number")
		return
	}
	value, reason := exactNonNegativeInt(it.ReadNumber().String())
	if reason != "" {
		*invalid = true
		o.source(path, "invalid", nil)
		o.issue(path, reason)
		return
	}
	o.source(path, "reported", &value)
	if *target != nil && **target != value {
		*invalid = true
		o.issue(path, "conflicting_count")
		return
	}
	*target = &value
}

func (o *NonChatObservation) readCounters(it *jsoniter.Iterator, path string, billed bool) {
	block, invalid := &o.Processing, &o.processingInvalid
	if billed {
		block, invalid = &o.Billed, &o.billedInvalid
	}
	if it.WhatIsNext() == jsoniter.NilValue {
		it.ReadNil()
		o.source(path, "null", nil)
		return
	}
	if it.WhatIsNext() != jsoniter.ObjectValue {
		it.Skip()
		*invalid = true
		o.issue(path, "expected_object")
		return
	}
	it.ReadObjectCB(func(it *jsoniter.Iterator, key string) bool {
		switch key {
		case "input_tokens", "prompt_tokens":
			o.readCounter(it, path+"."+key, &block.InputTokens, invalid)
		case "output_tokens", "completion_tokens":
			o.readCounter(it, path+"."+key, &block.OutputTokens, invalid)
		case "total_tokens":
			o.readCounter(it, path+"."+key, &block.TotalTokens, invalid)
		case "search_units":
			if !billed {
				it.Skip()
				break
			}
			if it.WhatIsNext() == jsoniter.NilValue {
				it.ReadNil()
				break
			}
			if it.WhatIsNext() != jsoniter.NumberValue {
				it.Skip()
				o.issue(path+"."+key, "expected_number")
				break
			}
			v, e := strconv.ParseFloat(it.ReadNumber().String(), 64)
			if e != nil || math.IsInf(v, 0) || math.IsNaN(v) || v < 0 {
				o.issue(path+"."+key, "invalid_billing_unit")
				break
			}
			if o.Units.SearchUnits != nil && *o.Units.SearchUnits != v {
				o.issue(path+"."+key, "conflicting_billing_unit")
				break
			}
			o.Units.SearchUnits = &v
		default:
			it.Skip()
		}
		return true
	})
}

func (o *NonChatObservation) readResults(it *jsoniter.Iterator, key, endpoint string) {
	if it.WhatIsNext() != jsoniter.ArrayValue {
		it.Skip()
		o.issue(key, "expected_array")
		return
	}
	var n int64
	var dimension *int64
	dimensionsKnown := true
	valid := true
	it.ReadArrayCB(func(it *jsoniter.Iterator) bool {
		n++
		if endpoint != "embeddings" {
			if it.WhatIsNext() != jsoniter.ObjectValue {
				valid = false
			}
			it.Skip()
			return true
		}
		if it.WhatIsNext() != jsoniter.ObjectValue {
			it.Skip()
			valid = false
			return true
		}
		var d *int64
		hasVector := false
		it.ReadObjectCB(func(it *jsoniter.Iterator, field string) bool {
			if field != "embedding" {
				it.Skip()
				return true
			}
			hasVector = true
			if it.WhatIsNext() == jsoniter.ArrayValue {
				var width int64
				numeric := true
				it.ReadArrayCB(func(it *jsoniter.Iterator) bool {
					if it.WhatIsNext() != jsoniter.NumberValue {
						numeric = false
					}
					it.Skip()
					width++
					return true
				})
				if numeric {
					d = &width
				} else {
					valid = false
				}
			} else if it.WhatIsNext() == jsoniter.StringValue {
				it.Skip()
			} else {
				it.Skip()
				valid = false
			}
			return true
		})
		if !hasVector {
			valid = false
		}
		if d == nil {
			dimensionsKnown = false
		} else if dimension == nil {
			dimension = d
		} else if *dimension != *d {
			dimensionsKnown = false
		}
		return true
	})
	if it.Error != nil {
		return
	}
	if endpoint == "embeddings" {
		if valid {
			o.Summary.Counts.EmbeddingVectors = &n
		} else {
			o.issue(key, "invalid_vector_representation")
		}
		if valid && dimensionsKnown {
			o.Summary.Counts.EmbeddingDimensions = dimension
		}
	} else if valid {
		o.Summary.Counts.RerankResults = &n
	} else {
		o.issue(key, "invalid_result_representation")
	}
}

// ObserveNonChatResponse consumes a reader incrementally. Unused documents and
// vectors are skipped, not retained; callers can tee the original bytes to the
// client. Syntax failures are diagnostics and never replacement response bodies.
func ObserveNonChatResponse(reader io.Reader, endpoint string) *NonChatObservation {
	o := &NonChatObservation{ResponseState: "complete"}
	it := jsoniter.Parse(jsoniter.ConfigCompatibleWithStandardLibrary, reader, 32*1024)
	if it.WhatIsNext() != jsoniter.ObjectValue {
		it.Skip()
		o.ResponseState = "invalid_json"
	} else {
		it.ReadObjectCB(func(it *jsoniter.Iterator, key string) bool {
			switch key {
			case "usage", "tokens":
				o.readCounters(it, key, false)
			case "meta":
				if it.WhatIsNext() != jsoniter.ObjectValue {
					it.Skip()
					break
				}
				it.ReadObjectCB(func(it *jsoniter.Iterator, key string) bool {
					switch key {
					case "tokens":
						o.readCounters(it, "meta.tokens", false)
					case "billed_units":
						o.readCounters(it, "meta.billed_units", true)
					default:
						it.Skip()
					}
					return true
				})
			case "error":
				o.providerError = it.WhatIsNext() != jsoniter.NilValue
				it.Skip()
			case "data":
				if endpoint == "embeddings" {
					o.readResults(it, key, endpoint)
				} else {
					it.Skip()
				}
			case "results":
				if endpoint == "rerank" {
					o.readResults(it, key, endpoint)
				} else {
					it.Skip()
				}
			default:
				it.Skip()
			}
			return true
		})
	}
	if it.Error != nil {
		o.ResponseState = "invalid_json"
	} else if it.WhatIsNext() != jsoniter.InvalidValue || it.Error != io.EOF {
		o.ResponseState = "invalid_json"
	}
	if o.ResponseState != "complete" {
		o.issue("$", "invalid_or_incomplete_json")
		o.Summary.Counts = NonChatCounts{}
	}
	if o.providerError {
		o.ResponseState = "provider_error"
	}
	o.finalize()
	return o
}

func counterSum(c NonChatCounters) (int64, bool) {
	var a, b int64
	if c.InputTokens != nil {
		a = *c.InputTokens
	}
	if c.OutputTokens != nil {
		b = *c.OutputTokens
	}
	if b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}
func hasCounters(c NonChatCounters) bool {
	return c.InputTokens != nil || c.OutputTokens != nil || c.TotalTokens != nil
}
func (o *NonChatObservation) totalPath(group string) string {
	for _, source := range o.Sources {
		if strings.HasSuffix(source.Path, ".total_tokens") && (strings.HasPrefix(source.Path, "meta.billed_units.") == (group == "meta.billed_units")) {
			return source.Path
		}
	}
	return group
}
func (o *NonChatObservation) finalize() {
	for _, b := range []struct {
		c       NonChatCounters
		invalid *bool
		path    string
	}{{o.Processing, &o.processingInvalid, "processing"}, {o.Billed, &o.billedInvalid, "meta.billed_units"}} {
		sum, ok := counterSum(b.c)
		if !ok {
			*b.invalid = true
			o.issue(b.path, "count_sum_overflow")
		} else if b.c.TotalTokens != nil && *b.c.TotalTokens < sum {
			*b.invalid = true
			o.issue(o.totalPath(b.path), "total_below_known_directions")
		}
	}
	o.Summary.Basis = "missing"
	o.Summary.Completeness = "missing"
	if o.ResponseState != "complete" {
		o.Summary.Completeness = "invalid"
		return
	}
	c, invalid := o.Processing, o.processingInvalid
	if hasCounters(c) || invalid {
		o.Summary.Basis = "processing"
	} else if hasCounters(o.Billed) || o.billedInvalid {
		c, invalid = o.Billed, o.billedInvalid
		o.Summary.Basis = "billed"
	}
	if invalid {
		o.Summary.Completeness = "invalid"
		return
	}
	if !hasCounters(c) {
		return
	}
	o.Summary.NonChatCounters = c
	if c.TotalTokens != nil {
		o.Summary.Completeness = "complete"
	} else {
		total, _ := counterSum(c)
		o.Summary.TotalTokens = &total
		o.Summary.Completeness = "partial"
		if c.InputTokens != nil && c.OutputTokens != nil {
			o.Summary.Completeness = "complete"
		}
	}
}

func (o *NonChatObservation) Usage() *Usage {
	if o == nil || o.Summary.TotalTokens == nil {
		return nil
	}
	u := &Usage{TotalTokens: *o.Summary.TotalTokens}
	if o.Summary.InputTokens != nil {
		u.PromptTokens = *o.Summary.InputTokens
	}
	if o.Summary.OutputTokens != nil {
		u.CompletionTokens = *o.Summary.OutputTokens
	}
	return u
}

// ObserveNonChatRequest counts only the supported public input representations.
func ObserveNonChatRequest(body []byte, endpoint string) NonChatCounts {
	var req struct {
		Input     json.RawMessage   `json:"input"`
		Documents []json.RawMessage `json:"documents"`
		TopN      *int64            `json:"top_n"`
	}
	if json.Unmarshal(body, &req) != nil {
		return NonChatCounts{}
	}
	counts := NonChatCounts{}
	if endpoint == "rerank" {
		if req.Documents != nil {
			n := int64(len(req.Documents))
			counts.RerankDocuments = &n
		}
		counts.RerankTopN = req.TopN
		return counts
	}
	var input EmbeddingInput
	if len(req.Input) > 0 && input.UnmarshalJSON(req.Input) == nil {
		n := input.ItemCount()
		counts.EmbeddingInputItems = &n
	}
	return counts
}
