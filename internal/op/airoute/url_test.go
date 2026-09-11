package airoute

import (
	"net/http"
	"net/url"
	"testing"
)

func TestJoinAIRouteChatCompletionsURL(t *testing.T) {
	testCases := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{name: "octopus public API", baseURL: "https://octopus.example/v1", wantURL: "https://octopus.example/v1/chat/completions"},
		{name: "trailing slash", baseURL: " https://octopus.example/v1/ ", wantURL: "https://octopus.example/v1/chat/completions"},
		{name: "explicit endpoint", baseURL: "https://provider.example/custom/chat/completions/", wantURL: "https://provider.example/custom/chat/completions"},
		{name: "external prefix preserved", baseURL: "https://provider.example/api/openai/v2", wantURL: "https://provider.example/api/openai/v2/chat/completions"},
		{name: "query preserved", baseURL: "https://provider.example/deployment?api-version=2026-01-01", wantURL: "https://provider.example/deployment/chat/completions?api-version=2026-01-01"},
		{name: "bare origin does not imply v1", baseURL: "https://provider.example", wantURL: "https://provider.example/chat/completions"},
		{name: "local public endpoint", baseURL: "http://localhost:8080/v1", wantURL: "http://localhost:8080/v1/chat/completions"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(subtest *testing.T) {
			actualURL, err := joinAIRouteChatCompletionsURL(testCase.baseURL)
			if err != nil {
				subtest.Fatalf("joinAIRouteChatCompletionsURL() error = %v", err)
			}
			if actualURL != testCase.wantURL {
				subtest.Fatalf("joined URL = %q, want %q", actualURL, testCase.wantURL)
			}
		})
	}
	for _, invalidURL := range []string{"", "/v1", "octopus.example/v1", "https://"} {
		if _, err := joinAIRouteChatCompletionsURL(invalidURL); err == nil {
			t.Errorf("joinAIRouteChatCompletionsURL(%q) should reject incomplete address", invalidURL)
		}
	}
}

func TestAIRouteHTTPClientKeepsExplicitProxySelection(t *testing.T) {
	directClient, err := newAIRouteHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	directTransport := directClient.Transport.(*http.Transport)
	if directTransport.Proxy != nil {
		t.Fatal("empty system proxy should use a direct connection, not environment proxy settings")
	}

	proxyAddress := "http://proxy.example:8080"
	proxiedClient, err := newAIRouteHTTPClient(proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	proxiedTransport := proxiedClient.Transport.(*http.Transport)
	if proxiedTransport.Proxy == nil {
		t.Fatal("configured system proxy should be retained")
	}
	requestURL, err := url.Parse("https://octopus.example/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	selectedProxy, err := proxiedTransport.Proxy(&http.Request{URL: requestURL})
	if err != nil || selectedProxy == nil || selectedProxy.String() != proxyAddress {
		t.Fatalf("selected proxy = %v, error = %v; want %s", selectedProxy, err, proxyAddress)
	}
}
