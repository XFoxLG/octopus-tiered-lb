package model

import (
	"encoding/json"
	"testing"
)

func TestLogWireIDsPreserveExactIdentity(t *testing.T) {
	const id int64 = 117392002743437824
	for _, value := range []any{RelayLog{ID: id}, RelayLogListItem{ID: id}, ErrorLog{ID: id}, RelayLogContentRef{ID: id, RelayLogID: id}} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["id_str"] != "117392002743437824" {
			t.Fatalf("missing exact identity: %s", body)
		}
		if _, ok := decoded["id"].(float64); !ok {
			t.Fatalf("legacy numeric ID was removed: %s", body)
		}
	}
}

func TestCapturedContentWireIDDoesNotHideText(t *testing.T) {
	body, err := json.Marshal(RelayLogCapturedContent{RelayLogContentRef: RelayLogContentRef{ID: 42, RelayLogID: 117392002743437824}, Text: "visible", Data: []byte("private")})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["text"] != "visible" || decoded["id_str"] != "42" || decoded["relay_log_id_str"] != "117392002743437824" {
		t.Fatalf("incorrect content envelope: %s", body)
	}
	if _, ok := decoded["Data"]; ok {
		t.Fatalf("private payload exposed: %s", body)
	}
}
