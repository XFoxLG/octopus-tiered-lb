package thinking

import "testing"

func TestParseModelSuffix(t *testing.T) {
	cases := []struct {
		model      string
		wantName   string
		hasSuffix  bool
		wantEffort string
		wantBudget int64
	}{
		// recognised level suffixes
		{"claude-sonnet-4-5(high)", "claude-sonnet-4-5", true, "high", 0},
		{"gemini-3.1-pro-preview(xhigh)", "gemini-3.1-pro-preview", true, "xhigh", 0},
		{"gpt-5.2(MAX)", "gpt-5.2", true, "max", 0},
		{"model(minimal)", "model", true, "minimal", 0},
		{"model( minimal )", "model", true, "minimal", 0}, // surrounding spaces are tolerated

		// numeric budgets (leading zeros accepted)
		{"model(8192)", "model", true, "", 8192},
		{"model(08192)", "model", true, "", 8192},
		{"model(0)", "model", true, "", 0},
		{"model(-1)", "model", true, "", -1},
		{"model(-5)", "model(-5)", false, "", 0},
		{"model(99999999999999999999999)", "model(99999999999999999999999)", false, "", 0},

		// special control values
		{"model(none)", "model", true, "", 0},
		{"model(NONE)", "model", true, "", 0},
		{"model(auto)", "model", true, "", -1},
		{"model(AUTO)", "model", true, "", -1},

		// unrecognised / malformed → untouched
		{"gemini-2.5-pro", "gemini-2.5-pro", false, "", 0},
		{"foo(experimental)", "foo(experimental)", false, "", 0},
		{"foo()", "foo()", false, "", 0},
		{"(high)", "(high)", false, "", 0},
		{"model(high", "model(high", false, "", 0},
		{"model)high(", "model)high(", false, "", 0},
		{"", "", false, "", 0},
	}
	for _, c := range cases {
		got := ParseModelSuffix(c.model)
		if got.ModelName != c.wantName || got.HasSuffix != c.hasSuffix ||
			got.Effort != c.wantEffort || got.Budget != c.wantBudget {
			t.Errorf("ParseModelSuffix(%q) = %+v, want {%q %v %q %d}",
				c.model, got, c.wantName, c.hasSuffix, c.wantEffort, c.wantBudget)
		}
	}
}

// TestParseModelSuffixUsesLastParenthesisedGroup pins the CLI-compatible
// behaviour: only the final group counts, so a name with its own parentheses
// keeps the earlier part verbatim.
func TestParseModelSuffixUsesLastParenthesisedGroup(t *testing.T) {
	got := ParseModelSuffix("vendor/foo(experimental)(high)")
	if !got.HasSuffix || got.ModelName != "vendor/foo(experimental)" || got.Effort != "high" {
		t.Fatalf("got %+v", got)
	}
}
