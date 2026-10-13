package cmd

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"strings"

	"github.com/lingyuins/octopus/internal/utils/log"
)

const (
	// Render Free gives the container 512MB. 420MiB leaves headroom for
	// non-heap memory (goroutine stacks, runtime bookkeeping) while still
	// letting Go use most of the budget for request buffers and log payloads.
	defaultMemoryLimitBytes int64 = 420 << 20
	defaultGCPercent              = 100

	metricMemoryLimitName = "/gc/gomemlimit:bytes"
	metricGCPercentName   = "/gc/gogc:percent"
)

// applyRuntimeMemoryLimits installs a process-level memory guard so the Go
// runtime keeps the heap inside Render Free's 512MB container instead of
// letting RSS climb until the platform kills the process. Explicit
// GOMEMLIMIT / GOGC environment variables always win (including "off").
func applyRuntimeMemoryLimits() {
	if value, ok := os.LookupEnv("GOMEMLIMIT"); ok && strings.TrimSpace(value) != "" {
		log.Infof("memory guard: GOMEMLIMIT provided by environment (%s), keeping it", value)
	} else {
		debug.SetMemoryLimit(defaultMemoryLimitBytes)
		log.Infof("memory guard: GOMEMLIMIT not set, applying %d MiB default", defaultMemoryLimitBytes>>20)
	}

	if value, ok := os.LookupEnv("GOGC"); ok && strings.TrimSpace(value) != "" {
		log.Infof("memory guard: GOGC provided by environment (%s), keeping it", value)
	} else {
		debug.SetGCPercent(defaultGCPercent)
		log.Infof("memory guard: GOGC not set, applying %d default", defaultGCPercent)
	}

	log.Infof("memory guard effective: GOMEMLIMIT=%s GOGC=%s",
		runtimeMemoryLimitLabel(), runtimeGCPercentLabel())
}

func readRuntimeMetricValue(name string) (uint64, bool) {
	samples := []metrics.Sample{{Name: name}}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindBad {
		return 0, false
	}
	return samples[0].Value.Uint64(), true
}

func runtimeMemoryLimitLabel() string {
	value, ok := readRuntimeMetricValue(metricMemoryLimitName)
	if !ok || value == math.MaxUint64 {
		return "off"
	}
	return fmt.Sprintf("%d MiB", value>>20)
}

func runtimeGCPercentLabel() string {
	value, ok := readRuntimeMetricValue(metricGCPercentName)
	if !ok || value == math.MaxUint64 {
		return "off"
	}
	return fmt.Sprintf("%d", value)
}
