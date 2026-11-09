package model

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNonChatTimingIncludesHeaderWaitAndDistinguishesFirstByte(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://example.test/embeddings", nil)
	req = StartNonChatTiming(req)
	timing := req.Context().Value(nonChatTimingKey{}).(*nonChatTiming)
	timing.start = time.Now().Add(-2 * time.Second)
	response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}
	AttachNonChatTiming(response, req)
	first := make([]byte, 1)
	if _, err := response.Body.Read(first); err != nil {
		t.Fatal(err)
	}
	o := &NonChatObservation{}
	ApplyNonChatTiming(o, response)
	if o.Summary.FirstByteMS == nil || *o.Summary.FirstByteMS < 2000 || o.Summary.ResponseMS != nil {
		t.Fatalf("first byte not distinct from complete response: %+v", o.Summary)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	ApplyNonChatTiming(o, response)
	if o.Summary.ResponseMS == nil || *o.Summary.ResponseMS < *o.Summary.FirstByteMS {
		t.Fatalf("missing response completion: %+v", o.Summary)
	}
}
