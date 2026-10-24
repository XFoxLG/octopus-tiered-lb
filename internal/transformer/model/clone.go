package model

import (
	"net/url"
	"reflect"

	transformer "github.com/lingyuins/octopus/internal/transformer"
)

// CloneForAttempt returns a private working copy of a shared read-only request.
//
// The relay parses one request and reuses that single instance across retries,
// hedges and per-channel attempts. Every attempt then injects into it:
// param_override, group reasoning defaults, transformer metadata, and the
// outbound adapters' in-place sanitising. Those injections must never leak back
// into the shared source, otherwise a later attempt would mistake the injected
// value for something the client set and drop its own override.
//
// The clone therefore owns a fresh allocation for every mutable nested
// reference in InternalLLMRequest and its message/tool/format sub-trees.
// Plain scalars copy with the struct assignment. RawRequest is intentionally
// shared: it is immutable logging data and copying it once per attempt would add
// megabytes of churn for no isolation benefit (callers must treat it as read
// only, which the relay already does).
func CloneForAttempt(request *InternalLLMRequest) *InternalLLMRequest {
	if request == nil {
		return nil
	}

	cloned := *request

	cloned.Messages = cloneMessagesForAttempt(request.Messages)
	cloned.EmbeddingInput = cloneEmbeddingInput(request.EmbeddingInput)
	cloned.EmbeddingDimensions = clonePointer(request.EmbeddingDimensions)
	cloned.EmbeddingEncodingFormat = clonePointer(request.EmbeddingEncodingFormat)
	cloned.FrequencyPenalty = clonePointer(request.FrequencyPenalty)
	cloned.Logprobs = clonePointer(request.Logprobs)
	cloned.MaxCompletionTokens = clonePointer(request.MaxCompletionTokens)
	cloned.MaxTokens = clonePointer(request.MaxTokens)
	cloned.PresencePenalty = clonePointer(request.PresencePenalty)
	cloned.Seed = clonePointer(request.Seed)
	cloned.Store = clonePointer(request.Store)
	cloned.Temperature = clonePointer(request.Temperature)
	cloned.TopLogprobs = clonePointer(request.TopLogprobs)
	cloned.TopP = clonePointer(request.TopP)
	cloned.PromptCacheKey = clonePointer(request.PromptCacheKey)
	cloned.SafetyIdentifier = clonePointer(request.SafetyIdentifier)
	cloned.User = clonePointer(request.User)
	cloned.LogitBias = cloneMap(request.LogitBias)
	cloned.Metadata = cloneMap(request.Metadata)
	cloned.Modalities = cloneSlice(request.Modalities)
	cloned.Audio = clonePointer(request.Audio)
	cloned.ReasoningBudget = clonePointer(request.ReasoningBudget)
	cloned.EnableThinking = clonePointer(request.EnableThinking)
	cloned.ServiceTier = clonePointer(request.ServiceTier)
	cloned.Stop = cloneStop(request.Stop)
	cloned.Stream = clonePointer(request.Stream)
	cloned.StreamOptions = clonePointer(request.StreamOptions)
	cloned.Prediction = clonePrediction(request.Prediction)
	cloned.ParallelToolCalls = clonePointer(request.ParallelToolCalls)
	cloned.Tools = cloneTools(request.Tools)
	cloned.ToolChoice = cloneToolChoice(request.ToolChoice)
	cloned.ResponseFormat = cloneResponseFormat(request.ResponseFormat)
	cloned.ExtraBody = cloneRawMessage(request.ExtraBody)
	cloned.TransformerMetadata = cloneMap(request.TransformerMetadata)
	cloned.TransformOptions = cloneTransformOptions(request.TransformOptions)
	cloned.Include = cloneSlice(request.Include)
	cloned.Query = cloneQuery(request.Query)

	return &cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// cloneSlice preserves nil-vs-empty so a cloned request stays value-equal to its
// source; make(..., 0) keeps a non-nil empty slice non-nil.
func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func cloneMap[K comparable, V any](values map[K]V) map[K]V {
	if values == nil {
		return nil
	}
	cloned := make(map[K]V, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneRawMessage(message transformer.RawMessage) transformer.RawMessage {
	if message == nil {
		return nil
	}
	cloned := make(transformer.RawMessage, len(message))
	copy(cloned, message)
	return cloned
}

// cloneAny deep-copies the JSON-decoded values carried by permissive
// map[string]any fields (e.g. ImageGeneration.InputImageMask) so nested objects
// and arrays are not shared between attempts.
func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		if typed == nil {
			return []any(nil)
		}
		cloned := make([]any, len(typed))
		for i := range typed {
			cloned[i] = cloneAny(typed[i])
		}
		return cloned
	case []string:
		return cloneSlice(typed)
	default:
		if value == nil {
			return nil
		}
		return cloneReferenceValue(reflect.ValueOf(value)).Interface()
	}
}

// Typed extension values may be constructed by adapters rather than a JSON
// decoder. Copy their reference graph too, preserving the original types.
func cloneReferenceValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		if value.Kind() == reflect.Pointer {
			cloned := reflect.New(value.Type().Elem())
			cloned.Elem().Set(cloneReferenceValue(value.Elem()))
			return cloned
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneReferenceValue(value.Elem()))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			cloned.SetMapIndex(iter.Key(), cloneReferenceValue(iter.Value()))
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneReferenceValue(value.Index(i)))
		}
		return cloned
	case reflect.Struct, reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(value)
		if value.Kind() == reflect.Array {
			for i := 0; i < value.Len(); i++ {
				cloned.Index(i).Set(cloneReferenceValue(value.Index(i)))
			}
		} else {
			for i := 0; i < value.NumField(); i++ {
				if value.Type().Field(i).PkgPath == "" {
					cloned.Field(i).Set(cloneReferenceValue(value.Field(i)))
				}
			}
		}
		return cloned
	default:
		return value
	}
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = cloneAny(value)
	}
	return cloned
}

func cloneQuery(values url.Values) url.Values {
	if values == nil {
		return nil
	}
	cloned := make(url.Values, len(values))
	for key, list := range values {
		cloned[key] = cloneSlice(list)
	}
	return cloned
}

func cloneMessagesForAttempt(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i := range messages {
		cloned[i] = cloneMessageForAttempt(messages[i])
	}
	return cloned
}

func cloneMessageForAttempt(message Message) Message {
	cloned := message

	if message.Content.Content != nil {
		content := *message.Content.Content
		cloned.Content.Content = &content
	}
	cloned.Content.MultipleContent = cloneContentParts(message.Content.MultipleContent)
	cloned.Name = clonePointer(message.Name)
	cloned.MessageIndex = clonePointer(message.MessageIndex)
	cloned.ToolCallID = clonePointer(message.ToolCallID)
	cloned.ToolCallName = clonePointer(message.ToolCallName)
	cloned.ToolCallIsError = clonePointer(message.ToolCallIsError)
	cloned.ToolCalls = cloneToolCalls(message.ToolCalls)
	cloned.Images = cloneContentParts(message.Images)
	cloned.Annotations = cloneSlice(message.Annotations)
	cloned.Audio = clonePointer(message.Audio)
	cloned.ReasoningContent = clonePointer(message.ReasoningContent)
	cloned.Reasoning = clonePointer(message.Reasoning)
	cloned.ReasoningSignature = clonePointer(message.ReasoningSignature)
	cloned.CacheControl = clonePointer(message.CacheControl)

	return cloned
}

func cloneContentParts(parts []MessageContentPart) []MessageContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]MessageContentPart, len(parts))
	for i := range parts {
		cloned[i] = cloneContentPart(parts[i])
	}
	return cloned
}

func cloneContentPart(part MessageContentPart) MessageContentPart {
	cloned := part

	cloned.Text = clonePointer(part.Text)
	if part.ImageURL != nil {
		imageURL := *part.ImageURL
		imageURL.Detail = clonePointer(part.ImageURL.Detail)
		cloned.ImageURL = &imageURL
	}
	cloned.Audio = clonePointer(part.Audio)
	cloned.File = clonePointer(part.File)
	cloned.CacheControl = clonePointer(part.CacheControl)

	return cloned
}

func cloneToolCalls(toolCalls []ToolCall) []ToolCall {
	if toolCalls == nil {
		return nil
	}
	cloned := make([]ToolCall, len(toolCalls))
	for i := range toolCalls {
		cloned[i] = cloneToolCall(toolCalls[i])
	}
	return cloned
}

func cloneToolCall(toolCall ToolCall) ToolCall {
	cloned := toolCall
	cloned.CacheControl = clonePointer(toolCall.CacheControl)
	return cloned
}

func cloneTools(tools []Tool) []Tool {
	if tools == nil {
		return nil
	}
	cloned := make([]Tool, len(tools))
	for i := range tools {
		cloned[i] = cloneTool(tools[i])
	}
	return cloned
}

func cloneTool(tool Tool) Tool {
	cloned := tool
	cloned.Function.Parameters = cloneRawMessage(tool.Function.Parameters)
	cloned.Function.Strict = clonePointer(tool.Function.Strict)
	cloned.ImageGeneration = cloneImageGeneration(tool.ImageGeneration)
	cloned.CacheControl = clonePointer(tool.CacheControl)
	return cloned
}

func cloneImageGeneration(imageGeneration *ImageGeneration) *ImageGeneration {
	if imageGeneration == nil {
		return nil
	}
	cloned := *imageGeneration
	cloned.OutputCompression = clonePointer(imageGeneration.OutputCompression)
	cloned.PartialImages = clonePointer(imageGeneration.PartialImages)
	cloned.InputImageMask = cloneAnyMap(imageGeneration.InputImageMask)
	return &cloned
}

func cloneToolChoice(toolChoice *ToolChoice) *ToolChoice {
	if toolChoice == nil {
		return nil
	}
	cloned := *toolChoice
	cloned.ToolChoice = clonePointer(toolChoice.ToolChoice)
	if toolChoice.NamedToolChoice != nil {
		named := *toolChoice.NamedToolChoice
		cloned.NamedToolChoice = &named
	}
	return &cloned
}

func cloneResponseFormat(responseFormat *ResponseFormat) *ResponseFormat {
	if responseFormat == nil {
		return nil
	}
	cloned := *responseFormat
	if responseFormat.JSONSchema != nil {
		schema := *responseFormat.JSONSchema
		schema.Schema = cloneRawMessage(responseFormat.JSONSchema.Schema)
		schema.Strict = clonePointer(responseFormat.JSONSchema.Strict)
		cloned.JSONSchema = &schema
	}
	return &cloned
}

func cloneStop(stop *Stop) *Stop {
	if stop == nil {
		return nil
	}
	cloned := *stop
	cloned.Stop = clonePointer(stop.Stop)
	cloned.MultipleStop = cloneSlice(stop.MultipleStop)
	return &cloned
}

func clonePrediction(prediction *ChatCompletionPredictionContent) *ChatCompletionPredictionContent {
	if prediction == nil {
		return nil
	}
	cloned := *prediction
	cloned.Content = cloneSlice(prediction.Content)
	return &cloned
}

func cloneEmbeddingInput(input *EmbeddingInput) *EmbeddingInput {
	if input == nil {
		return nil
	}
	cloned := *input
	cloned.Single = clonePointer(input.Single)
	cloned.Multiple = cloneSlice(input.Multiple)
	return &cloned
}

func cloneTransformOptions(options TransformOptions) TransformOptions {
	options.ArrayInputs = clonePointer(options.ArrayInputs)
	return options
}
