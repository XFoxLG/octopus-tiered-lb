package semantic_cache

import (
	"testing"
	"time"
)

func TestEmbeddingServiceChangeInvalidatesOldVectorSpace(t *testing.T) {
	defer Reset()
	configuration := RuntimeConfig{Enabled: true, MaxEntries: 10, Threshold: 0.98, TTL: time.Minute, EmbeddingBaseURL: "https://embedding.example.test/v1", EmbeddingModel: "model-a"}
	ApplyRuntimeConfig(configuration)
	Store("namespace", "question", []byte(`{"answer":"old"}`), []float64{1, 0})
	ApplyRuntimeConfig(configuration)
	if _, found := Lookup("namespace", []float64{1, 0}); !found {
		t.Fatal("unchanged embedding configuration should preserve entries")
	}
	configuration.EmbeddingModel = "model-b"
	ApplyRuntimeConfig(configuration)
	if _, found := Lookup("namespace", []float64{1, 0}); found {
		t.Fatal("vectors from another embedding model must never be compared")
	}
	Store("namespace", "question", []byte(`{"answer":"old"}`), []float64{1, 0})
	configuration.EmbeddingBaseURL = "https://different.example.test/v1"
	ApplyRuntimeConfig(configuration)
	if _, found := Lookup("namespace", []float64{1, 0}); found {
		t.Fatal("changing embedding provider must also invalidate old vectors")
	}
}
