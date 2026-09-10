package client

import (
	"net/http"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

func TestShortProxyClientTracksItsOwnConfiguration(t *testing.T) {
	configurationCache := setting.GetCache()
	originalURL, existed := configurationCache.Get(model.SettingKeyProxyURL)
	clientLock.Lock()
	originalClient, originalShortClient := systemProxyClient, shortTimeoutProxyClient
	originalStamp, originalShortStamp := systemProxyURL, shortTimeoutProxyURL
	systemProxyClient, shortTimeoutProxyClient = nil, nil
	systemProxyURL, shortTimeoutProxyURL = "", ""
	clientLock.Unlock()
	t.Cleanup(func() {
		if existed {
			configurationCache.Set(model.SettingKeyProxyURL, originalURL)
		} else {
			configurationCache.Del(model.SettingKeyProxyURL)
		}
		clientLock.Lock()
		systemProxyClient, shortTimeoutProxyClient = originalClient, originalShortClient
		systemProxyURL, shortTimeoutProxyURL = originalStamp, originalShortStamp
		clientLock.Unlock()
	})
	configurationCache.Set(model.SettingKeyProxyURL, "http://127.0.0.1:12341")
	firstShort, err := GetHTTPClientShortTimeout(true)
	if err != nil {
		t.Fatal(err)
	}
	configurationCache.Set(model.SettingKeyProxyURL, "http://127.0.0.1:12342")
	if _, err := GetHTTPClientSystemProxy(true); err != nil {
		t.Fatal(err)
	}
	secondShort, err := GetHTTPClientShortTimeout(true)
	if err != nil {
		t.Fatal(err)
	}
	if firstShort == secondShort {
		t.Fatal("normal client refresh must not make a stale short client appear current")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example.test", nil)
	selectedProxy, err := secondShort.Transport.(*http.Transport).Proxy(request)
	if err != nil || selectedProxy.String() != "http://127.0.0.1:12342" {
		t.Fatal("short requests retained the previous proxy address")
	}
}
