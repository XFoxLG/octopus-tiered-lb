package semantic_cache

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// CacheEntry holds a cached request/response pair with its embedding.
//
// Entries are stored behind pointers (see SemanticCache.entries) and the
// mutable hit metadata (lastAccessNs / HitCount) is accessed atomically so
// that Lookup can scan and record hits under a read lock without serializing
// concurrent lookups. The immutable fields (Namespace, ResponseJSON,
// Embedding, CreatedAt) are written once at Store time and never mutated in
// place afterwards, so they are safe to read after releasing the read lock.
type CacheEntry struct {
	Namespace    string
	RequestKey   string
	ResponseJSON []byte
	Embedding    []float64
	CreatedAt    time.Time
	lastAccessNs atomic.Int64 // unix nanoseconds of last access; updated on hit
	HitCount     atomic.Int64
}

// SemanticCache is an in-memory vector store with cosine similarity lookup.
type SemanticCache struct {
	mu               sync.RWMutex
	entries          []*CacheEntry
	maxEntries       int
	threshold        float64
	ttl              time.Duration
	hits             atomic.Int64
	misses           atomic.Int64
	embeddingBaseURL string
	embeddingModel   string
}

// globalCacheMu protects the globalCache pointer itself from concurrent read/write.
// The individual cache operations use their own internal mutex (SemanticCache.mu).
var globalCacheMu sync.RWMutex
var globalCache *SemanticCache

type RuntimeConfig struct {
	Enabled          bool
	MaxEntries       int
	Threshold        float64
	TTL              time.Duration
	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string
	EmbeddingTimeout time.Duration
}

// Init creates or reconfigures the global semantic cache.
func Init(maxEntries int, threshold float64, ttlSec int) {
	if maxEntries <= 0 {
		globalCacheMu.Lock()
		globalCache = nil
		globalCacheMu.Unlock()
		return
	}
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	if globalCache == nil || globalCache.maxEntries != maxEntries || globalCache.threshold != threshold || globalCache.ttl != time.Duration(ttlSec)*time.Second {
		globalCache = &SemanticCache{
			entries:    make([]*CacheEntry, 0, maxEntries),
			maxEntries: maxEntries,
			threshold:  threshold,
			ttl:        time.Duration(ttlSec) * time.Second,
		}
	}
}

// ApplyRuntimeConfig creates or reconfigures the global semantic cache from runtime settings.
// When the cache already exists and the size/threshold/TTL parameters are unchanged,
// the existing cache is reused so stored entries are not discarded.
func ApplyRuntimeConfig(cfg RuntimeConfig) {
	if !cfg.Enabled || cfg.MaxEntries <= 0 {
		Reset()
		return
	}

	ttl := cfg.TTL
	globalCacheMu.Lock()
	defer globalCacheMu.Unlock()
	if globalCache != nil &&
		globalCache.maxEntries == cfg.MaxEntries &&
		globalCache.threshold == cfg.Threshold &&
		globalCache.ttl == ttl &&
		globalCache.embeddingBaseURL == cfg.EmbeddingBaseURL &&
		globalCache.embeddingModel == cfg.EmbeddingModel {
		return
	}

	globalCache = &SemanticCache{
		entries:          make([]*CacheEntry, 0, cfg.MaxEntries),
		maxEntries:       cfg.MaxEntries,
		threshold:        cfg.Threshold,
		ttl:              ttl,
		embeddingBaseURL: cfg.EmbeddingBaseURL,
		embeddingModel:   cfg.EmbeddingModel,
	}
}

// Reset clears the cache and runtime configuration.
func Reset() {
	globalCacheMu.Lock()
	globalCache = nil
	globalCacheMu.Unlock()
}

// Lookup finds the best matching cache entry for the given embedding.
// Returns the response JSON and true if a match above threshold is found.
//
// The cosine-similarity scan runs under a read lock so that concurrent
// lookups proceed in parallel rather than serializing on a single mutex.
// Expired entries are skipped inline; structural removal of expired entries
// is deferred to Store/Stats (which hold the write lock). Hit metadata is
// updated atomically, so recording a hit does not require the write lock.
func Lookup(namespace string, embedding []float64) (responseJSON []byte, found bool) {
	globalCacheMu.RLock()
	cache := globalCache
	globalCacheMu.RUnlock()
	if cache == nil {
		return nil, false
	}

	cache.mu.RLock()
	var best *CacheEntry
	bestSim := -1.0
	nowNs := time.Now().UnixNano()
	ttl := int64(cache.ttl)
	for _, entry := range cache.entries {
		if entry.Namespace != namespace {
			continue
		}
		if ttl > 0 && nowNs-entry.lastAccessNs.Load() >= ttl {
			continue // expired; skip without mutating the slice under RLock
		}
		sim := cosineSimilarity(embedding, entry.Embedding)
		if sim > bestSim {
			bestSim = sim
			best = entry
		}
	}
	threshold := cache.threshold
	cache.mu.RUnlock()

	if best != nil && bestSim >= threshold {
		best.HitCount.Add(1)
		best.lastAccessNs.Store(time.Now().UnixNano())
		cache.hits.Add(1)
		// ResponseJSON is immutable after Store, but callers may mutate the
		// returned slice, so hand back a copy.
		return append([]byte(nil), best.ResponseJSON...), true
	}

	cache.misses.Add(1)
	return nil, false
}

// Store adds a new entry to the cache. If the cache is full, the oldest entry is evicted.
func Store(namespace, requestKey string, responseJSON []byte, embedding []float64) {
	globalCacheMu.RLock()
	cache := globalCache
	globalCacheMu.RUnlock()
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.pruneExpiredLocked()

	now := time.Now()
	entry := &CacheEntry{
		Namespace:    namespace,
		RequestKey:   requestKey,
		ResponseJSON: append([]byte(nil), responseJSON...),
		Embedding:    cloneEmbedding(embedding),
		CreatedAt:    now,
	}
	entry.lastAccessNs.Store(now.UnixNano())

	if len(cache.entries) >= cache.maxEntries && len(cache.entries) > 0 {
		// Evict the least-recently-accessed entry.
		oldestIdx := 0
		oldestNs := cache.entries[0].lastAccessNs.Load()
		for i, e := range cache.entries {
			if ns := e.lastAccessNs.Load(); ns < oldestNs {
				oldestNs = ns
				oldestIdx = i
			}
		}
		cache.entries[oldestIdx] = entry
	} else {
		cache.entries = append(cache.entries, entry)
	}
}

// Stats returns hit/miss counts and current cache size.
func Stats() (hits, misses int64, size int) {
	globalCacheMu.RLock()
	cache := globalCache
	globalCacheMu.RUnlock()
	if cache == nil {
		return 0, 0, 0
	}
	cache.mu.Lock()
	cache.pruneExpiredLocked()
	size = len(cache.entries)
	cache.mu.Unlock()
	return cache.hits.Load(), cache.misses.Load(), size
}

// Clear empties the cache.
func Clear() {
	globalCacheMu.RLock()
	cache := globalCache
	globalCacheMu.RUnlock()
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.entries = make([]*CacheEntry, 0, cache.maxEntries)
	cache.mu.Unlock()
	cache.hits.Store(0)
	cache.misses.Store(0)
}

// Enabled returns true if the semantic cache is initialized and active.
func Enabled() bool {
	globalCacheMu.RLock()
	cache := globalCache
	globalCacheMu.RUnlock()
	return cache != nil
}

func (sc *SemanticCache) pruneExpiredLocked() {
	if sc.ttl <= 0 {
		return
	}
	nowNs := time.Now().UnixNano()
	ttl := int64(sc.ttl)
	n := 0
	for _, entry := range sc.entries {
		if nowNs-entry.lastAccessNs.Load() < ttl {
			sc.entries[n] = entry
			n++
		}
	}
	sc.entries = sc.entries[:n]
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func cloneEmbedding(src []float64) []float64 {
	dst := make([]float64, len(src))
	copy(dst, src)
	return dst
}
