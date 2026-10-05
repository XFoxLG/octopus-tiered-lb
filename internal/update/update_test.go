package update

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/conf"
)

func TestUpdateURLs_DefaultToForkRepository(t *testing.T) {
	original := conf.AppConfig.External
	conf.AppConfig.External = conf.External{}
	defer func() { conf.AppConfig.External = original }()

	const wantDownloadURL = "https://github.com/XFoxLG/octopus-tiered-lb/releases/latest/download"
	const wantAPIURL = "https://api.github.com/repos/XFoxLG/octopus-tiered-lb/releases/latest"
	if got := getUpdateURL(); got != wantDownloadURL {
		t.Fatalf("getUpdateURL() = %q, want %q", got, wantDownloadURL)
	}
	if got := getUpdateAPIURL(); got != wantAPIURL {
		t.Fatalf("getUpdateAPIURL() = %q, want %q", got, wantAPIURL)
	}
}

func TestUpdateURLs_ConfigOverrideStillWins(t *testing.T) {
	original := conf.AppConfig.External
	conf.AppConfig.External = conf.External{
		UpdateURL:    "https://example.com/octopus/download",
		UpdateAPIURL: "https://example.com/api/octopus/latest",
	}
	defer func() { conf.AppConfig.External = original }()

	if got, want := getUpdateURL(), conf.AppConfig.External.UpdateURL; got != want {
		t.Fatalf("getUpdateURL() = %q, want %q", got, want)
	}
	if got, want := getUpdateAPIURL(), conf.AppConfig.External.UpdateAPIURL; got != want {
		t.Fatalf("getUpdateAPIURL() = %q, want %q", got, want)
	}
}

func TestDoRequest_AllowsLargeDownloadWithoutLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", maxUpdateAPIResponseBytes+1024))
	}))
	defer server.Close()

	data, err := doRequest(server.URL, false, 0, "")
	if err != nil {
		t.Fatalf("doRequest() unexpected error: %v", err)
	}

	if got, want := len(data), maxUpdateAPIResponseBytes+1024; got != want {
		t.Fatalf("download size = %d, want %d", got, want)
	}
}

func TestDoRequest_RejectsOversizedAPIResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("b", maxUpdateAPIResponseBytes+1))
	}))
	defer server.Close()

	_, err := doRequest(server.URL, false, maxUpdateAPIResponseBytes, "update API response")
	if err == nil {
		t.Fatal("doRequest() error = nil, want oversized response error")
	}

	want := fmt.Sprintf("update API response exceeds %d bytes limit", maxUpdateAPIResponseBytes)
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
