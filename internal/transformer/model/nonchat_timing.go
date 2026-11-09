package model

import (
	"context"
	"io"
	"net/http"
	"time"
)

type nonChatTimingKey struct{}
type nonChatTiming struct {
	start    time.Time
	first    *int64
	complete *int64
}
type nonChatTimedBody struct {
	io.ReadCloser
	timing *nonChatTiming
}

func (b *nonChatTimedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if n > 0 && b.timing.first == nil {
		v := time.Since(b.timing.start).Milliseconds()
		b.timing.first = &v
	}
	if e == io.EOF && b.timing.complete == nil {
		v := time.Since(b.timing.start).Milliseconds()
		b.timing.complete = &v
	}
	return n, e
}

// Dispatch time includes connection and response-header wait.
func StartNonChatTiming(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), nonChatTimingKey{}, &nonChatTiming{start: time.Now()}))
}
func AttachNonChatTiming(resp *http.Response, req *http.Request) {
	timing, _ := req.Context().Value(nonChatTimingKey{}).(*nonChatTiming)
	if timing == nil {
		return
	}
	resp.Request = req
	resp.Body = &nonChatTimedBody{resp.Body, timing}
}
func ApplyNonChatTiming(o *NonChatObservation, resp *http.Response) {
	if resp.Request == nil {
		return
	}
	timing, _ := resp.Request.Context().Value(nonChatTimingKey{}).(*nonChatTiming)
	if timing == nil {
		return
	}
	o.Summary.FirstByteMS = timing.first
	o.Summary.ResponseMS = timing.complete
}
