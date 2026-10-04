package httpx

import (
	"net/http"

	"github.com/lingyuins/octopus/internal/conf"
)

func DefaultUserAgent() string {
	return "octopus-ly/" + conf.Version
}

// WithUserAgent preserves explicit upstream profiles and supplies the application
// identity when a request would otherwise use Go's default User-Agent.
func WithUserAgent(base http.RoundTripper) http.RoundTripper {
	if _, ok := base.(*userAgentTransport); ok {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &userAgentTransport{base: base}
}

func BaseTransport(transport http.RoundTripper) http.RoundTripper {
	for {
		wrapped, ok := transport.(*userAgentTransport)
		if !ok {
			return transport
		}
		transport = wrapped.base
	}
}

type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		// RoundTrippers must not mutate the caller's request, including its headers.
		req = req.Clone(req.Context())
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("User-Agent", DefaultUserAgent())
	}
	return t.base.RoundTrip(req)
}

func (t *userAgentTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
