package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/conf"
)

type recordingTransport struct {
	request *http.Request
	closed  bool
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.request = req
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
}

func (t *recordingTransport) CloseIdleConnections() {
	t.closed = true
}

func TestUserAgentTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
		want   string
	}{
		{"missing", make(http.Header), DefaultUserAgent()},
		{"nil headers", nil, DefaultUserAgent()},
		{"empty", http.Header{"User-Agent": {""}}, DefaultUserAgent()},
		{"custom", http.Header{"User-Agent": {"upstream-required/1.0"}}, "upstream-required/1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header = tc.header
			before := req.UserAgent()
			base := &recordingTransport{}
			wrapped := WithUserAgent(base)
			resp, err := wrapped.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got := base.request.UserAgent(); got != tc.want {
				t.Fatalf("User-Agent = %q, want %q", got, tc.want)
			}
			if req.UserAgent() != before {
				t.Fatal("transport mutated caller headers")
			}
			if BaseTransport(wrapped) != base || WithUserAgent(wrapped) != wrapped {
				t.Fatal("transport wrapping must preserve the base and be idempotent")
			}
			client := &http.Client{Transport: wrapped}
			client.CloseIdleConnections()
			if !base.closed {
				t.Fatal("CloseIdleConnections was not forwarded")
			}
		})
	}
}

func TestDefaultUserAgentUsesBuildVersion(t *testing.T) {
	before := conf.Version
	conf.Version = "v9.8.7-test"
	t.Cleanup(func() { conf.Version = before })
	if got := DefaultUserAgent(); got != "octopus-ly/v9.8.7-test" {
		t.Fatalf("DefaultUserAgent = %q", got)
	}
}

func TestNilTransportUsesHTTPDefault(t *testing.T) {
	if got := BaseTransport(WithUserAgent(nil)); got != http.DefaultTransport {
		t.Fatalf("base transport = %T, want http.DefaultTransport", got)
	}
}

func TestUserAgentOverHTTPProtocolsAndRedirects(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		name := "http1"
		if http2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.UserAgent(); got != DefaultUserAgent() {
					t.Errorf("wire User-Agent = %q, want %q", got, DefaultUserAgent())
					http.Error(w, "blocked user agent", http.StatusUnauthorized)
					return
				}
				if http2 && r.ProtoMajor != 2 {
					t.Errorf("protocol = %s, want HTTP/2", r.Proto)
				}
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			server.EnableHTTP2 = http2
			server.StartTLS()
			t.Cleanup(server.Close)
			client := server.Client()
			client.Transport = WithUserAgent(client.Transport)
			t.Cleanup(client.CloseIdleConnections)
			resp, err := client.Get(server.URL + "/redirect")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/final" {
				t.Fatalf("redirect response = %d at %s", resp.StatusCode, resp.Request.URL)
			}
		})
	}
}
