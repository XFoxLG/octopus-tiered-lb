package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/utils/httpx"
)

func TestClientFactoriesSupplyDefaultUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != httpx.DefaultUserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, httpx.DefaultUserAgent())
			http.Error(w, "blocked user agent", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		name string
		make func() (*http.Client, error)
	}{
		{"direct", func() (*http.Client, error) { return GetHTTPClientSystemProxy(false) }},
		{"short direct", func() (*http.Client, error) { return GetHTTPClientShortTimeout(false) }},
		{"proxy", func() (*http.Client, error) { return GetHTTPClientCustomProxy(server.URL) }},
		{"short proxy", func() (*http.Client, error) { return GetHTTPClientCustomProxyWithTimeout(server.URL, 30*time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := tc.make()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.CloseIdleConnections)
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("response status = %d", resp.StatusCode)
			}
		})
	}
}
