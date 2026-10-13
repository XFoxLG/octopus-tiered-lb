package cmd

import (
	"math"
	"runtime/debug"
	"testing"
)

func TestApplyRuntimeMemoryLimitsAppliesDefaultsWhenEnvUnset(t *testing.T) {
	originalLimit := debug.SetMemoryLimit(-1)
	originalGC, originalGCKnown := readRuntimeMetricValue(metricGCPercentName)
	t.Cleanup(func() {
		debug.SetMemoryLimit(originalLimit)
		if originalGCKnown && originalGC != math.MaxUint64 {
			debug.SetGCPercent(int(originalGC))
		}
	})

	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("GOGC", "")

	applyRuntimeMemoryLimits()

	if got := debug.SetMemoryLimit(-1); got != defaultMemoryLimitBytes {
		t.Fatalf("memory limit = %d, want %d", got, defaultMemoryLimitBytes)
	}
	if got, ok := readRuntimeMetricValue(metricGCPercentName); !ok || got != defaultGCPercent {
		t.Fatalf("GOGC percent = %d (known=%v), want %d", got, ok, defaultGCPercent)
	}
}

func TestApplyRuntimeMemoryLimitsKeepsEnvironmentOverrides(t *testing.T) {
	originalLimit := debug.SetMemoryLimit(-1)
	originalGC, originalGCKnown := readRuntimeMetricValue(metricGCPercentName)
	t.Cleanup(func() {
		debug.SetMemoryLimit(originalLimit)
		if originalGCKnown && originalGC != math.MaxUint64 {
			debug.SetGCPercent(int(originalGC))
		}
	})

	// Simulate an operator-provided override. The runtime reads the real
	// environment at startup, so the assertion here is that the guard leaves
	// the process values untouched when the variables are present.
	t.Setenv("GOMEMLIMIT", "off")
	t.Setenv("GOGC", "off")

	beforeLimit := debug.SetMemoryLimit(-1)
	beforeGC, _ := readRuntimeMetricValue(metricGCPercentName)

	applyRuntimeMemoryLimits()

	if got := debug.SetMemoryLimit(-1); got != beforeLimit {
		t.Fatalf("memory limit changed with GOMEMLIMIT override: %d -> %d", beforeLimit, got)
	}
	if got, ok := readRuntimeMetricValue(metricGCPercentName); ok && got != beforeGC {
		t.Fatalf("GOGC percent changed with GOGC override: %d -> %d", beforeGC, got)
	}
}
