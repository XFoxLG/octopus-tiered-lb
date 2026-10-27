package group

import "testing"

func TestNormalizeGroupDefaultReasoningEffort(t *testing.T) {
	valid := map[string]string{
		"":          "",
		"  AUTO ":   "auto",
		" minimal ": "minimal",
		"LOW":       "low",
		"medium":    "medium",
		"high":      "high",
		"xhigh":     "xhigh",
		"max":       "max",
	}
	for input, want := range valid {
		got, err := normalizeGroupDefaultReasoningEffort(input)
		if err != nil {
			t.Fatalf("normalizeGroupDefaultReasoningEffort(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Errorf("normalizeGroupDefaultReasoningEffort(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := normalizeGroupDefaultReasoningEffort("unsupported"); err == nil {
		t.Fatal("unsupported effort must be rejected")
	}
}
