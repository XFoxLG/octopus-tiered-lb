package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/redis/go-redis/v9"
)

// redisStats 是 StatsStore 的 Redis 实现。
//
// 语义：Redis 作为 source of truth，每次 *Update 实时增量写入，SaveDB 时
// SnapshotAll 读全量落盘 DB。计数器字段用 HINCRBY/HINCRBYFLOAT，max 字段
// （LatencyP50/95/99、Ftut*）用 Lua 原子 max 脚本（GET→比较→SET 必须原子，
// 否则并发写会丢失 max）。
//
// key 布局：octopus:stats:{scope}:{id}（Hash）。
type redisStats struct {
	c *redis.Client
}

func newRedisStats(c *redis.Client) *redisStats { return &redisStats{c: c} }

// counterFields 是用 HINCRBY 累加的整型计数器字段。
var counterFields = []string{
	"input_token", "output_token",
	"unattributed_token", "billed_token",
	"usage_reported_requests", "usage_partial_requests", "usage_billed_requests", "usage_invalid_requests",
	"embedding_input_items", "embedding_input_observed", "embedding_vectors", "embedding_vectors_observed",
	"rerank_documents", "rerank_documents_observed", "rerank_results", "rerank_results_observed",
	"wait_time", "request_success", "request_failed",
	"histogram_lt_100", "histogram_100_500", "histogram_500_1k", "histogram_1k_5k", "histogram_gt_5k",
}

// maxFields 是取最大值的字段（LatencyP50/95/99、FtutAvg/P50/P95/P99），
// 与 counterFields/floatFields 分开处理，走 Lua 原子 max 脚本。
var maxFields = []string{
	"latency_p50", "latency_p95", "latency_p99",
	"ftut_avg", "ftut_p50", "ftut_p95", "ftut_p99",
}

// statsMaxScript 原子地比较并更新一组 max 字段。
// KEYS[1] = hash key
// ARGV: 成对的 [field, value, field, value, ...]（仅 int64 max 字段）
// 对每个 field：若 ARGV value > 现有值则 HSET。并发写不会丢失 max（脚本原子）。
var statsMaxScript = redis.NewScript(`
local key = KEYS[1]
local changed = 0
for i = 1, #ARGV, 2 do
  local field = ARGV[i]
  local newval = tonumber(ARGV[i+1])
  if newval == nil then
    newval = 0
  end
  local cur = tonumber(redis.call('HGET', key, field))
  if cur == nil or newval > cur then
    redis.call('HSET', key, field, tostring(newval))
    changed = changed + 1
  end
end
return changed
`)

func statsKey(scope, id string) string {
	return withPrefix("stats:" + scope + ":" + id)
}

func (s *redisStats) IncrMetrics(ctx context.Context, scope, id string, m model.StatsMetrics) error {
	key := statsKey(scope, id)

	// 计数器字段：HINCRBY pipeline（int 计数器 + float cost）。
	pipe := s.c.Pipeline()
	intVals := []int64{
		m.InputToken, m.OutputToken,
		m.UnattributedToken, m.BilledToken,
		m.UsageReportedRequests, m.UsagePartialRequests, m.UsageBilledRequests, m.UsageInvalidRequests,
		m.EmbeddingInputItems, m.EmbeddingInputObserved, m.EmbeddingVectors, m.EmbeddingVectorsObserved,
		m.RerankDocuments, m.RerankDocumentsObserved, m.RerankResults, m.RerankResultsObserved,
		m.WaitTime, m.RequestSuccess, m.RequestFailed,
		m.HistogramLt100, m.Histogram100to500, m.Histogram500to1k, m.Histogram1kto5k, m.HistogramGt5k,
	}
	for i, field := range counterFields {
		pipe.HIncrBy(ctx, key, field, intVals[i])
	}
	pipe.HIncrByFloat(ctx, key, "input_cost", m.InputCost)
	pipe.HIncrByFloat(ctx, key, "output_cost", m.OutputCost)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("stats incr pipeline: %w", err)
	}

	// max 字段：Lua 原子脚本（pipeline 不直接支持 NewScript，单独调用保证原子）。
	maxVals := []interface{}{
		"latency_p50", m.LatencyP50,
		"latency_p95", m.LatencyP95,
		"latency_p99", m.LatencyP99,
		"ftut_avg", m.FtutAvg,
		"ftut_p50", m.FtutP50,
		"ftut_p95", m.FtutP95,
		"ftut_p99", m.FtutP99,
	}
	if _, err := statsMaxScript.Run(ctx, s.c, []string{key}, maxVals...).Result(); err != nil {
		return fmt.Errorf("stats max script: %w", err)
	}
	return nil
}

func (s *redisStats) GetMetrics(ctx context.Context, scope, id string) (model.StatsMetrics, error) {
	raw, err := s.c.HGetAll(ctx, statsKey(scope, id)).Result()
	if err != nil {
		return model.StatsMetrics{}, fmt.Errorf("stats get: %w", err)
	}
	if len(raw) == 0 {
		return model.StatsMetrics{}, nil
	}
	return parseStatsHash(raw), nil
}

func (s *redisStats) SnapshotAll(ctx context.Context, scope string) (map[string]model.StatsMetrics, error) {
	prefix := withPrefix("stats:" + scope + ":") // "octopus:stats:{scope}:"
	pattern := prefix + "*"                      // SCAN MATCH 需通配符
	result := make(map[string]model.StatsMetrics)

	var cursor uint64
	for {
		keys, cur, err := s.c.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("stats snapshot scan: %w", err)
		}
		for _, key := range keys {
			raw, err := s.c.HGetAll(ctx, key).Result()
			if err != nil {
				return nil, fmt.Errorf("stats snapshot hgetall %s: %w", key, err)
			}
			if len(raw) == 0 {
				continue
			}
			// 去掉 "octopus:stats:{scope}:" 前缀得到 id
			id := key[len(prefix):]
			result[id] = parseStatsHash(raw)
		}
		if cursor = cur; cursor == 0 {
			break
		}
	}
	return result, nil
}

func (s *redisStats) Delete(ctx context.Context, scope, id string) error {
	return s.c.Del(ctx, statsKey(scope, id)).Err()
}

// parseStatsHash 将 Redis Hash 解析回 StatsMetrics。
func parseStatsHash(raw map[string]string) model.StatsMetrics {
	var m model.StatsMetrics
	m.InputToken, _ = strconv.ParseInt(raw["input_token"], 10, 64)
	m.OutputToken, _ = strconv.ParseInt(raw["output_token"], 10, 64)
	m.UnattributedToken, _ = strconv.ParseInt(raw["unattributed_token"], 10, 64)
	m.BilledToken, _ = strconv.ParseInt(raw["billed_token"], 10, 64)
	m.UsageReportedRequests, _ = strconv.ParseInt(raw["usage_reported_requests"], 10, 64)
	m.UsagePartialRequests, _ = strconv.ParseInt(raw["usage_partial_requests"], 10, 64)
	m.UsageBilledRequests, _ = strconv.ParseInt(raw["usage_billed_requests"], 10, 64)
	m.UsageInvalidRequests, _ = strconv.ParseInt(raw["usage_invalid_requests"], 10, 64)
	m.EmbeddingInputItems, _ = strconv.ParseInt(raw["embedding_input_items"], 10, 64)
	m.EmbeddingInputObserved, _ = strconv.ParseInt(raw["embedding_input_observed"], 10, 64)
	m.EmbeddingVectors, _ = strconv.ParseInt(raw["embedding_vectors"], 10, 64)
	m.EmbeddingVectorsObserved, _ = strconv.ParseInt(raw["embedding_vectors_observed"], 10, 64)
	m.RerankDocuments, _ = strconv.ParseInt(raw["rerank_documents"], 10, 64)
	m.RerankDocumentsObserved, _ = strconv.ParseInt(raw["rerank_documents_observed"], 10, 64)
	m.RerankResults, _ = strconv.ParseInt(raw["rerank_results"], 10, 64)
	m.RerankResultsObserved, _ = strconv.ParseInt(raw["rerank_results_observed"], 10, 64)
	m.InputCost, _ = strconv.ParseFloat(raw["input_cost"], 64)
	m.OutputCost, _ = strconv.ParseFloat(raw["output_cost"], 64)
	m.WaitTime, _ = strconv.ParseInt(raw["wait_time"], 10, 64)
	m.RequestSuccess, _ = strconv.ParseInt(raw["request_success"], 10, 64)
	m.RequestFailed, _ = strconv.ParseInt(raw["request_failed"], 10, 64)
	m.LatencyP50, _ = strconv.ParseInt(raw["latency_p50"], 10, 64)
	m.LatencyP95, _ = strconv.ParseInt(raw["latency_p95"], 10, 64)
	m.LatencyP99, _ = strconv.ParseInt(raw["latency_p99"], 10, 64)
	m.FtutAvg, _ = strconv.ParseInt(raw["ftut_avg"], 10, 64)
	m.FtutP50, _ = strconv.ParseInt(raw["ftut_p50"], 10, 64)
	m.FtutP95, _ = strconv.ParseInt(raw["ftut_p95"], 10, 64)
	m.FtutP99, _ = strconv.ParseInt(raw["ftut_p99"], 10, 64)
	m.HistogramLt100, _ = strconv.ParseInt(raw["histogram_lt_100"], 10, 64)
	m.Histogram100to500, _ = strconv.ParseInt(raw["histogram_100_500"], 10, 64)
	m.Histogram500to1k, _ = strconv.ParseInt(raw["histogram_500_1k"], 10, 64)
	m.Histogram1kto5k, _ = strconv.ParseInt(raw["histogram_1k_5k"], 10, 64)
	m.HistogramGt5k, _ = strconv.ParseInt(raw["histogram_gt_5k"], 10, 64)
	return m
}
