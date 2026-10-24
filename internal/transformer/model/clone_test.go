package model

import (
	"fmt"
	"reflect"
	"testing"
)

// fullyPopulatedRequestForClone fills every mutable nested reference of
// InternalLLMRequest, including the fields the pre-REQ-02 relay clone omitted
// (Tools/Function.Parameters, ImageGeneration.InputImageMask, ToolChoice,
// ResponseFormat.JSONSchema, EmbeddingInput.Single, Stop.Stop, per-message and
// per-content-part CacheControl, ImageURL.Detail, Prediction.Content, and the
// scalar pointers such as Temperature/PromptCacheKey).
func fullyPopulatedRequestForClone() *InternalLLMRequest {
	text := "hello"
	detail := "high"
	content := "answer"
	reasoning := "thinking"
	reasoningContent := "deep"
	signature := "sig"
	name := "finish"
	toolCallID := "call_1"
	toolCallName := "lookup"
	toolCallIsError := true
	messageIndex := 3
	stop := "STOP"
	promptKey := "cache-key"
	safetyID := "safety"
	user := "user-1"
	schema := []byte(`{"type":"object"}`)
	strict := true
	paramStrict := true
	parallel := true
	stream := true
	streamUsage := true
	arrayInputs := true
	enableThinking := true
	dimensions := int64(256)
	encoding := "float"
	frequencyPenalty := 0.1
	presencePenalty := 0.2
	temperature := 0.7
	topP := 0.9
	topLogprobs := int64(2)
	maxTokens := int64(1024)
	maxCompletion := int64(2048)
	seed := int64(42)
	store := true
	logprobs := true
	reasoningBudget := int64(4096)
	outputCompression := int64(80)
	partialImages := int64(1)
	embeddingSingle := "embed me"

	return &InternalLLMRequest{
		Model: "clone-fixture",
		Messages: []Message{
			{
				Role: "assistant",
				Content: MessageContent{
					MultipleContent: []MessageContentPart{
						{
							Type:         "text",
							Text:         &text,
							CacheControl: &CacheControl{Type: "ephemeral", TTL: "5m"},
						},
						{
							Type:     "image_url",
							ImageURL: &ImageURL{URL: "https://example.test/a.png", Detail: &detail},
							Audio:    &Audio{Format: "wav", Data: "AAAA"},
							File:     &File{Filename: "a.txt", FileData: "BBBB"},
						},
					},
				},
				Name:            &name,
				MessageIndex:    &messageIndex,
				ToolCallID:      &toolCallID,
				ToolCallName:    &toolCallName,
				ToolCallIsError: &toolCallIsError,
				CacheControl:    &CacheControl{Type: "ephemeral"},
				ToolCalls: []ToolCall{
					{
						ID:           "call_1",
						Type:         "function",
						Function:     FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`},
						Index:        0,
						CacheControl: &CacheControl{Type: "ephemeral"},
					},
				},
				Images:      []MessageContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.test/b.png"}}},
				Annotations: []Annotation{{Type: "url_citation", URL: "https://example.test"}},
				Audio: &struct {
					Data       string `json:"data,omitempty"`
					ExpiresAt  int64  `json:"expires_at,omitempty"`
					ID         string `json:"id,omitempty"`
					Transcript string `json:"transcript,omitempty"`
				}{Data: "AAAA", ExpiresAt: 1, ID: "audio-1", Transcript: "hi"},
				ReasoningContent:   &reasoningContent,
				Reasoning:          &reasoning,
				ReasoningSignature: &signature,
			},
			{
				Role:    "user",
				Content: MessageContent{Content: &content},
			},
		},
		EmbeddingInput:          &EmbeddingInput{Single: &embeddingSingle, Multiple: []string{"a", "b"}},
		EmbeddingDimensions:     &dimensions,
		EmbeddingEncodingFormat: &encoding,
		FrequencyPenalty:        &frequencyPenalty,
		Logprobs:                &logprobs,
		MaxCompletionTokens:     &maxCompletion,
		MaxTokens:               &maxTokens,
		PresencePenalty:         &presencePenalty,
		Seed:                    &seed,
		Store:                   &store,
		Temperature:             &temperature,
		TopLogprobs:             &topLogprobs,
		TopP:                    &topP,
		PromptCacheKey:          &promptKey,
		SafetyIdentifier:        &safetyID,
		User:                    &user,
		LogitBias:               map[string]int64{"50256": -100},
		Metadata:                map[string]string{"trace": "1"},
		Modalities:              []string{"text", "audio"},
		Audio:                   &ChatCompletionAudioParam{Voice: "alloy", Format: "mp3"},
		ReasoningEffort:         "medium",
		ReasoningExplicit:       true,
		ReasoningBudget:         &reasoningBudget,
		AdaptiveThinking:        true,
		EnableThinking:          &enableThinking,
		ServiceTier:             stringPtr("default"),
		Stop:                    &Stop{Stop: &stop, MultipleStop: []string{"A", "B"}},
		Stream:                  &stream,
		StreamOptions:           &StreamOptions{IncludeUsage: streamUsage},
		Prediction: &ChatCompletionPredictionContent{
			Type:    "content",
			Content: []PredictionContentPart{{Type: "text", Text: "predicted"}},
		},
		ParallelToolCalls: &parallel,
		Tools: []Tool{
			{
				Type: "function",
				Function: Function{
					Name:        "lookup",
					Description: "look things up",
					Parameters:  []byte(`{"type":"object"}`),
					Strict:      &paramStrict,
				},
				ImageGeneration: &ImageGeneration{
					Background:        "opaque",
					InputImageMask:    map[string]any{"nested": map[string]any{"k": []any{"a", "b"}}},
					OutputCompression: &outputCompression,
					PartialImages:     &partialImages,
					Size:              "1024x1024",
				},
				CacheControl: &CacheControl{Type: "ephemeral"},
			},
		},
		ToolChoice: &ToolChoice{
			ToolChoice:      stringPtr("auto"),
			NamedToolChoice: &NamedToolChoice{Type: "function", Function: ToolFunction{Name: "lookup"}},
		},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &ResponseFormatJSONSchema{
				Name:        "answer",
				Description: "the answer",
				Schema:      schema,
				Strict:      &strict,
			},
		},
		ExtraBody:           []byte(`{"extra":true}`),
		RawRequest:          []byte(`{"model":"clone-fixture"}`),
		RawAPIFormat:        APIFormatAnthropicMessage,
		TransformerMetadata: map[string]string{"group_endpoint_type": "openai"},
		TransformOptions:    TransformOptions{ArrayInputs: &arrayInputs},
		Include:             []string{"reasoning.encrypted_content"},
		Query:               map[string][]string{"api-version": {"2024-10-01"}},
		RawPath:             "/v1/messages",
		ConversationID:      "conv-1",
		ResumeFromEventID:   7,
	}
}

func stringPtr(value string) *string { return &value }

// TestCloneForAttemptDeepEqualsAndSharesNoReferences proves the clone is
// value-equal to its source while every mutable nested reference is a distinct
// allocation: no pointer, map, slice backing array or interface payload is
// shared, so an injection on one attempt can never reach another attempt's copy.
func TestCloneForAttemptDeepEqualsAndSharesNoReferences(t *testing.T) {
	source := fullyPopulatedRequestForClone()
	cloned := CloneForAttempt(source)

	if !reflect.DeepEqual(source, cloned) {
		t.Fatalf("clone is not value-equal to its source\nsource: %+v\nclone:  %+v", source, cloned)
	}

	checker := &sharedReferenceChecker{t: t, skipFields: map[string]bool{"RawRequest": true}}
	checker.walk("request", reflect.ValueOf(source), reflect.ValueOf(cloned))

	// RawRequest is intentionally shared (immutable logging data).
	if len(source.RawRequest) == 0 || &source.RawRequest[0] != &cloned.RawRequest[0] {
		t.Fatalf("RawRequest should be shared by design")
	}
}

// TestCloneForAttemptIsolatesPreviouslyAliasedFields mutates the nested state the
// pre-REQ-02 clone aliased and asserts the shared source stays untouched.
func TestCloneForAttemptIsolatesPreviouslyAliasedFields(t *testing.T) {
	source := fullyPopulatedRequestForClone()
	before := *CloneForAttempt(source)
	cloned := CloneForAttempt(source)

	*cloned.Tools[0].Function.Strict = false
	cloned.Tools[0].Function.Parameters[0] = 'X'
	*cloned.Tools[0].ImageGeneration.OutputCompression = 100
	*cloned.Tools[0].ImageGeneration.PartialImages = 9
	cloned.Tools[0].ImageGeneration.InputImageMask["nested"].(map[string]any)["k"] = "changed"
	cloned.Tools[0].CacheControl.Type = "changed"

	*cloned.ToolChoice.ToolChoice = "changed"
	cloned.ToolChoice.NamedToolChoice.Function.Name = "changed"

	cloned.ResponseFormat.JSONSchema.Schema[2] = 'X'
	*cloned.ResponseFormat.JSONSchema.Strict = false

	*cloned.EmbeddingInput.Single = "changed"
	cloned.EmbeddingInput.Multiple[0] = "changed"
	*cloned.EmbeddingDimensions = 1
	*cloned.EmbeddingEncodingFormat = "base64"

	*cloned.Stop.Stop = "changed"
	cloned.Stop.MultipleStop[0] = "changed"
	cloned.Prediction.Content[0].Text = "changed"

	cloned.Messages[0].CacheControl.Type = "changed"
	cloned.Messages[0].Content.MultipleContent[0].CacheControl.Type = "changed"
	*cloned.Messages[0].Content.MultipleContent[1].ImageURL.Detail = "changed"
	cloned.Messages[0].ToolCalls[0].CacheControl.Type = "changed"
	cloned.Messages[0].ToolCalls[0].Function.Name = "changed"
	cloned.Messages[0].Images[0].ImageURL.URL = "changed"
	cloned.Messages[0].Annotations[0].URL = "changed"
	*cloned.Messages[0].ReasoningSignature = "changed"
	*cloned.Messages[1].Content.Content = "changed"

	cloned.LogitBias["50256"] = 0
	cloned.Metadata["trace"] = "changed"
	cloned.Modalities[0] = "changed"
	cloned.TransformerMetadata["group_endpoint_type"] = "changed"
	cloned.Include[0] = "changed"
	cloned.Query["api-version"][0] = "changed"
	cloned.ExtraBody[2] = 'X'
	*cloned.TransformOptions.ArrayInputs = false
	*cloned.Temperature = 1.5
	*cloned.PromptCacheKey = "changed"
	*cloned.EnableThinking = false
	*cloned.ReasoningBudget = 1
	cloned.Messages[0].Audio.ID = "changed"

	if !reflect.DeepEqual(before, *source) {
		for _, field := range diffTopLevelFields(before, *source) {
			t.Errorf("source field %q leaked a per-attempt mutation", field)
		}
		t.Fatalf("request source was mutated through the clone")
	}
	if reflect.DeepEqual(*source, *cloned) {
		t.Fatalf("mutations did not land on the clone")
	}
}

func TestCloneForAttemptNilAndEmpty(t *testing.T) {
	if CloneForAttempt(nil) != nil {
		t.Fatalf("nil request must clone to nil")
	}

	empty := &InternalLLMRequest{Metadata: map[string]string{}, Include: []string{}}
	cloned := CloneForAttempt(empty)
	if !reflect.DeepEqual(empty, cloned) {
		t.Fatalf("empty containers must stay value-equal")
	}
	cloned.Metadata["added"] = "x"
	if len(empty.Metadata) != 0 {
		t.Fatalf("empty map was shared with the clone")
	}
}

func TestCloneForAttemptTypedExtensionAndSiblingIsolation(t *testing.T) {
	source := fullyPopulatedRequestForClone()
	value := 1
	source.Tools[0].ImageGeneration.InputImageMask["typed"] = map[string][]*int{"values": {&value}}
	source.Tools[0].ImageGeneration.InputImageMask["nil"] = []any(nil)
	first, second := CloneForAttempt(source), CloneForAttempt(source)
	*first.Tools[0].ImageGeneration.InputImageMask["typed"].(map[string][]*int)["values"][0] = 2
	first.Tools[0].Function.Parameters[0] = 'X'
	if value != 1 || *second.Tools[0].ImageGeneration.InputImageMask["typed"].(map[string][]*int)["values"][0] != 1 || second.Tools[0].Function.Parameters[0] == 'X' {
		t.Fatal("typed nested extension leaked between attempts")
	}
	if first.Tools[0].ImageGeneration.InputImageMask["nil"].([]any) != nil {
		t.Fatal("nil extension slice changed shape")
	}
}

func diffTopLevelFields(a, b InternalLLMRequest) []string {
	var fields []string
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	for i := 0; i < av.NumField(); i++ {
		if !reflect.DeepEqual(av.Field(i).Interface(), bv.Field(i).Interface()) {
			fields = append(fields, av.Type().Field(i).Name)
		}
	}
	return fields
}

type sharedReferenceChecker struct {
	t          errorReporter
	skipFields map[string]bool
}

// errorReporter keeps the checker reusable with a recording stub so the guard
// itself can be proven non-vacuous.
type errorReporter interface {
	Errorf(format string, args ...any)
}

// TestSharedReferenceCheckerDetectsAliasing is a negative control: the checker
// must flag a shallow copy, otherwise the isolation assertions above could pass
// for the wrong reason.
func TestSharedReferenceCheckerDetectsAliasing(t *testing.T) {
	source := fullyPopulatedRequestForClone()
	shallow := *source // shares every nested reference on purpose

	recorder := &recordingReporter{}
	checker := &sharedReferenceChecker{t: recorder, skipFields: map[string]bool{"RawRequest": true}}
	checker.walk("request", reflect.ValueOf(source), reflect.ValueOf(&shallow))

	if len(recorder.messages) == 0 {
		t.Fatalf("checker failed to detect a shallow copy; isolation assertions would be vacuous")
	}
}

type recordingReporter struct {
	messages []string
}

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (c *sharedReferenceChecker) walk(path string, original, cloned reflect.Value) {
	if !original.IsValid() || !cloned.IsValid() {
		return
	}

	switch original.Kind() {
	case reflect.Pointer, reflect.Interface:
		if original.IsNil() || cloned.IsNil() {
			if original.IsNil() != cloned.IsNil() {
				c.t.Errorf("%s: nil-ness differs between source and clone", path)
			}
			return
		}
		if original.Kind() == reflect.Pointer {
			if original.Pointer() == cloned.Pointer() {
				c.t.Errorf("%s: clone shares the same pointer as the source", path)
				return
			}
		}
		c.walk(path, original.Elem(), cloned.Elem())
	case reflect.Struct:
		for i := 0; i < original.NumField(); i++ {
			field := original.Type().Field(i)
			if field.PkgPath != "" || c.skipFields[field.Name] {
				continue
			}
			c.walk(path+"."+field.Name, original.Field(i), cloned.Field(i))
		}
	case reflect.Slice:
		if original.IsNil() || cloned.IsNil() {
			if original.IsNil() != cloned.IsNil() {
				c.t.Errorf("%s: nil-ness differs between source and clone", path)
			}
			return
		}
		if original.Len() > 0 && original.Pointer() == cloned.Pointer() {
			c.t.Errorf("%s: clone slice shares the source backing array", path)
		}
		for i := 0; i < original.Len(); i++ {
			c.walk(fmt.Sprintf("%s[%d]", path, i), original.Index(i), cloned.Index(i))
		}
	case reflect.Array:
		for i := 0; i < original.Len(); i++ {
			c.walk(fmt.Sprintf("%s[%d]", path, i), original.Index(i), cloned.Index(i))
		}
	case reflect.Map:
		if original.IsNil() || cloned.IsNil() {
			if original.IsNil() != cloned.IsNil() {
				c.t.Errorf("%s: nil-ness differs between source and clone", path)
			}
			return
		}
		if original.Pointer() == cloned.Pointer() {
			c.t.Errorf("%s: clone map shares the source map", path)
		}
		for _, key := range original.MapKeys() {
			c.walk(path+"["+fmt.Sprint(key.Interface())+"]", original.MapIndex(key), cloned.MapIndex(key))
		}
	default:
		// Scalars (string, bool, numbers) are value-typed and copy with the
		// struct assignment; there is nothing to share.
	}
}
