package openai

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// Chat upstreams end streams with a [DONE] marker, but that marker is not part
// of the Responses streaming contract. Converting Chat -> Responses must not
// append a foreign `data: [DONE]` frame after a canonical Responses terminal
// event.
func TestResponseInboundTransformStreamSwallowsChatDoneMarker(t *testing.T) {
	inbound := &ResponseInbound{}
	payload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		Object:           "[DONE]",
		SawTerminalEvent: true,
	})
	if err != nil {
		t.Fatalf("TransformStream([DONE]) error = %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("TransformStream([DONE]) emitted %q, want no bytes", payload)
	}
}
