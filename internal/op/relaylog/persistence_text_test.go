package relaylog

import "testing"

func TestRelayLogDatabaseTextPreservesValidUnicode(t *testing.T) {
	for _, value := range []string{"", "中文🙂\n\t", "replacement: �", `literal\xff`, `{"text":"line\u0000break"}`} {
		if got := escapeRelayLogDatabaseText(value); got != value {
			t.Fatalf("valid text changed: %q => %q", value, got)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"prefix\xff中文\x00", `prefix\xff中文\x00`},
		{"\xe4\xb8", `\xe4\xb8`},
		{"\xc0\x80", `\xc0\x80`},
	} {
		if got := escapeRelayLogDatabaseText(tc.input); got != tc.want {
			t.Fatalf("invalid text = %q, want %q", got, tc.want)
		}
	}
}
