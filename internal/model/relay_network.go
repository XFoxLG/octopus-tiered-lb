package model

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

// RelayNetworkObserver is attempt-local. The same observer is used by probes
// and relay so response-header arrival can never stand in for the body byte.
type RelayNetworkObserver struct {
	mu              sync.Mutex
	diagnostics     RelayNetworkDiagnostics
	headersObserved bool
}

func NewRelayNetworkObserver(source string) *RelayNetworkObserver {
	return &RelayNetworkObserver{diagnostics: RelayNetworkDiagnostics{TimingSource: source, PreparationAt: time.Now().UnixMilli()}}
}

func (o *RelayNetworkObserver) network(fn func(*RelayNetworkDiagnostics)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.headersObserved {
		fn(&o.diagnostics)
	}
}

func markNetworkTime(field *int64) {
	if *field == 0 {
		*field = time.Now().UnixMilli()
	}
}

func (o *RelayNetworkObserver) Context(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.DNSStartAt) })
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.DNSEndAt) })
		},
		ConnectStart: func(string, string) { o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.DialStartAt) }) },
		ConnectDone: func(string, string, error) {
			o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.DialEndAt) })
		},
		TLSHandshakeStart: func() { o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.TLSStartAt) }) },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.TLSEndAt) })
		},
		GotConn: func(info httptrace.GotConnInfo) {
			o.network(func(d *RelayNetworkDiagnostics) {
				if d.ConnectionReused == nil {
					reused := info.Reused
					d.ConnectionReused = &reused
				}
			})
		},
		GotFirstResponseByte: func() { o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.HeaderFirstByteAt) }) },
	})
}

func (o *RelayNetworkObserver) Dispatch() {
	o.network(func(d *RelayNetworkDiagnostics) { markNetworkTime(&d.DispatchAt) })
}

func (o *RelayNetworkObserver) Headers() {
	o.mu.Lock()
	defer o.mu.Unlock()
	markNetworkTime(&o.diagnostics.ResponseHeadersAt)
	o.headersObserved = true // subsequent Tasks GET/cancel are not submission phases
}

func (o *RelayNetworkObserver) ObserveHeaders(headers http.Header) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.diagnostics.RequestID = strings.TrimSpace(headers.Get("X-Request-ID"))
	o.diagnostics.CFRay = strings.TrimSpace(headers.Get("CF-Ray"))
	o.diagnostics.RndrID = strings.TrimSpace(headers.Get("Rndr-Id"))
}

func (o *RelayNetworkObserver) EffectiveOutput(reasoning, visible, terminal bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if reasoning {
		markNetworkTime(&o.diagnostics.FirstReasoningAt)
	}
	if visible {
		markNetworkTime(&o.diagnostics.FirstEffectiveOutputAt)
	}
	if terminal {
		markNetworkTime(&o.diagnostics.TerminalAt)
	}
}

func (o *RelayNetworkObserver) Cancelled() {
	o.mu.Lock()
	defer o.mu.Unlock()
	markNetworkTime(&o.diagnostics.CancelledAt)
}

func (o *RelayNetworkObserver) Snapshot() *RelayNetworkDiagnostics {
	o.mu.Lock()
	defer o.mu.Unlock()
	copy := o.diagnostics
	if copy.ConnectionReused != nil {
		reused := *copy.ConnectionReused
		copy.ConnectionReused = &reused
	}
	return &copy
}

func (o *RelayNetworkObserver) Body(body io.ReadCloser) io.ReadCloser {
	return &relayObservedBody{ReadCloser: body, observer: o}
}

type relayObservedBody struct {
	io.ReadCloser
	observer *RelayNetworkObserver
}

func (b *relayObservedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.observer.mu.Lock()
		markNetworkTime(&b.observer.diagnostics.FirstBodyByteAt)
		b.observer.mu.Unlock()
	}
	return n, err
}
