package balancer

import (
	"math"
	"math/rand"
	"sort"
	"sync/atomic"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

var roundRobinCounter uint64

// AutoPriceFunc 返回某模型的成本评分输入（0-1，越便宜越接近 1）。
// 由 relay/init_hooks.go 在启动时注入（balancer 不能 import price——price 依赖
// db 与 op/llm，会拉起 balancer 单测不需要的重量级依赖链）；nil 表示成本
// 因子未启用（B3）。注入方负责价格归一化。
var AutoPriceFunc func(modelName string) float64

// Balancer 根据负载均衡模式选择通道
type Balancer interface {
	// Candidates 返回按策略排序的候选列表
	// 调用方在遍历候选列表时自行检查熔断状态
	Candidates(items []model.GroupItem) []model.GroupItem
}

// GetBalancer 根据模式返回对应的负载均衡器
func GetBalancer(mode model.GroupMode) Balancer {
	switch mode {
	case model.GroupModeRoundRobin:
		return &RoundRobin{}
	case model.GroupModeRandom:
		return &Random{}
	case model.GroupModeFailover:
		return &Failover{}
	case model.GroupModeWeighted:
		return &Weighted{}
	case model.GroupModeAuto:
		return &Auto{}
	default:
		return &RoundRobin{}
	}
}

// RoundRobin 轮询：从上次位置开始轮转排列
type RoundRobin struct{}

func (b *RoundRobin) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	idx := int(atomic.AddUint64(&roundRobinCounter, 1) % uint64(n))
	result := make([]model.GroupItem, n)
	for i := 0; i < n; i++ {
		result[i] = items[(idx+i)%n]
	}
	return result
}

// Random 随机：随机打乱所有 items
type Random struct{}

func (b *Random) Candidates(items []model.GroupItem) []model.GroupItem {
	n := len(items)
	if n == 0 {
		return nil
	}
	result := make([]model.GroupItem, n)
	copy(result, items)
	rand.Shuffle(n, func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result
}

// Failover 故障转移：按优先级排序
type Failover struct{}

func (b *Failover) Candidates(items []model.GroupItem) []model.GroupItem {
	if len(items) == 0 {
		return nil
	}
	return sortByPriority(items)
}

// Weighted 加权分配：按权重做无放回加权随机排序
//
// index 0 的期望被选中概率严格等于 weight/totalWeight，后续位置是重试顺序。
// 早先的实现（520ba524）是 rand.Intn(totalWeight) 的真加权随机；大重构把接口从
// Select 换成 Candidates 后退化成 sortByWeight 降序排序，权重从「流量比例」变成
// 「优先顺序」——权重最高的渠道永远独占全部流量。这里恢复加权语义，同时保留故障
// 转移能力：某个候选失败后，调用方继续按列表顺序取下一个。
// 对比上游 eff51e23 的 smooth WRR 展开序列（O(n×权重和)、依赖全局轮转相位）：
// 无放回加权随机的长期流量比例一致，单次 O(n²)（候选数小），且每次请求重新随机，
// 不存在固定周期与跨实例同步问题，对 Render 免费容器的 CPU 更友好。
type Weighted struct{}

func (b *Weighted) Candidates(items []model.GroupItem) []model.GroupItem {
	if len(items) == 0 {
		return nil
	}

	// Treat non-positive weights as 1 so a misconfigured zero-weight row still
	// participates instead of being silently dropped from the pool.
	weights := make([]int, len(items))
	totalWeight := 0
	for i, item := range items {
		weight := item.Weight
		if weight <= 0 {
			weight = 1
		}
		weights[i] = weight
		totalWeight += weight
	}

	result := make([]model.GroupItem, 0, len(items))
	for remaining, total := weights, totalWeight; total > 0; {
		index := drawWeightedIndex(remaining, total)
		result = append(result, items[index])
		total -= remaining[index]
		remaining[index] = 0
	}
	return result
}

// drawWeightedIndex picks an index proportional to the remaining weights.
// remaining is mutated by the caller (zeroing taken slots); total is the sum of
// the remaining positive weights.
func drawWeightedIndex(remaining []int, total int) int {
	roll := rand.Intn(total)
	for i, weight := range remaining {
		if weight <= 0 {
			continue
		}
		roll -= weight
		if roll < 0 {
			return i
		}
	}
	// Unreachable unless the caller passed an inconsistent total; fall back to
	// the first still-available slot rather than panicking.
	for i, weight := range remaining {
		if weight > 0 {
			return i
		}
	}
	return 0
}

// Auto 自动策略：探索优先，基于成功率和延迟动态选择
// - 探索阶段：优先选择未达到最小样本数的渠道
// - 利用阶段：综合成功率和延迟计算评分
// - 相同评分时：按权重/优先级兜底
type Auto struct{}

type autoScoredItem struct {
	item         model.GroupItem
	score        float64
	totalSamples int
	explored     bool
}

func (b *Auto) Candidates(items []model.GroupItem) []model.GroupItem {
	if len(items) == 0 {
		return nil
	}

	minSamples := getMinSamples()
	timeWindow := getTimeWindow()
	latencyWeight := getLatencyWeight()
	ttftWeight := getAutoTTFTWeight()
	priceWeight := getAutoPriceWeight()

	// Calculate scores for each item
	scored := make([]autoScoredItem, len(items))
	for i, item := range items {
		stats := getOrCreateStats(item.ChannelID, item.ModelName)
		successRate, totalSamples := stats.GetStats(timeWindow)

		scored[i].item = item
		scored[i].totalSamples = totalSamples
		scored[i].explored = totalSamples >= minSamples

		if !scored[i].explored {
			// Exploration phase: items with fewer samples are tried first.
			scored[i].score = 0
		} else {
			// Exploitation phase: blend success rate with latency
			scored[i].score = successRate
			if latencyWeight > 0 {
				// B4：TTFT 权重启用且该渠道有 TTFT 样本时，用 TTFT EMA 替代总延迟
				// EMA 作为延迟评分输入（流式体感优先）；无样本回退 GetLatency()。
				latencyMs := stats.GetLatency()
				if ttftWeight > 0 {
					if ttftMs := stats.GetTTFT(); ttftMs > 0 {
						latencyMs = ttftMs
					}
				}
				latencyScore := normalizeLatency(latencyMs)
				scored[i].score = successRate*(1-latencyWeight) + latencyScore*latencyWeight
			}
			// B3：成本因子——价格目录有数据（AutoPriceFunc 非 nil 且返回 >0）时，
			// 按价格评分做第二轮混合；未注入/无价格数据时保持原评分。
			if priceWeight > 0 && AutoPriceFunc != nil {
				if priceScore := AutoPriceFunc(item.ModelName); priceScore > 0 {
					scored[i].score = scored[i].score*(1-priceWeight) + priceScore*priceWeight
				}
			}
		}
		// #133: 全熔断的渠道降权排末尾，不剔除（保留 HalfOpen 探测能力）。
		// 只读查询不触发 Open->HalfOpen 转换；SkipCircuitBreak 仍是最终硬过滤。
		if IsChannelAllKeysTripped(item.ChannelID, item.ModelName) {
			scored[i].score = math.Inf(-1)
		}
		if rateLimited, _ := IsChannelRateLimited(item.ChannelID, item.ModelName); rateLimited {
			// A channel-level 429 quarantine is stronger than a soft Auto score.
			// Keep the item for recovery/probe visibility, but place it last.
			scored[i].score = math.Inf(-1)
		}
	}

	// Sort: unexplored first, then by score descending
	sort.SliceStable(scored, func(i, j int) bool {
		// Hard health gates always outrank exploration. Otherwise an untrained
		// but fully quarantined channel could be selected before a healthy one.
		iQuarantined := math.IsInf(scored[i].score, -1)
		jQuarantined := math.IsInf(scored[j].score, -1)
		if iQuarantined != jQuarantined {
			return !iQuarantined
		}

		// Exploration priority: unexplored channels come first
		if scored[i].explored != scored[j].explored {
			return !scored[i].explored
		}

		// Exploration phase: prefer lower-sampled candidates first so other
		// candidates are not starved by weight/priority.
		if !scored[i].explored {
			if scored[i].totalSamples != scored[j].totalSamples {
				return scored[i].totalSamples < scored[j].totalSamples
			}
			return compareByWeightPriority(scored[i].item, scored[j].item)
		}

		// Both explored: sort by score descending
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}

		// Same score: prefer the candidate with fewer in-window samples. This
		// gives equally healthy candidates a chance to recover from temporary
		// starvation instead of repeatedly selecting the same high-weight item.
		if scored[i].totalSamples != scored[j].totalSamples {
			return scored[i].totalSamples < scored[j].totalSamples
		}

		// Same score: fall back to weight/priority
		return compareByWeightPriority(scored[i].item, scored[j].item)
	})

	// Extract sorted items
	result := make([]model.GroupItem, len(items))
	for i, s := range scored {
		result[i] = s.item
	}

	// 利用阶段后处理（探索阶段排序保持确定性，不受影响）：
	// a) 同分桶打散：评分差在 bucketTolerance 内的相邻候选划为同桶，桶内随机洗牌，
	//    消除「同分渠道轮流垄断流量」的确定性偏差（B2）。
	// b) softmax 探索：exploreRate>0 时按概率对整体做一次温度采样重排（B2）。
	autoPostSortShuffle(scored, result)
	return result
}

// compareByWeightPriority compares two items by weight (descending), then priority (ascending).
// Returns true if a should come before b.
func compareByWeightPriority(a, b model.GroupItem) bool {
	// Compare weight (higher weight first)
	leftWeight := a.Weight
	if leftWeight <= 0 {
		leftWeight = 1
	}
	rightWeight := b.Weight
	if rightWeight <= 0 {
		rightWeight = 1
	}
	if leftWeight != rightWeight {
		return leftWeight > rightWeight
	}

	// Compare priority (lower priority value first)
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}

	// Tie-breaker: channel ID, then model name
	if a.ChannelID != b.ChannelID {
		return a.ChannelID < b.ChannelID
	}
	return a.ModelName < b.ModelName
}

func sortByPriority(items []model.GroupItem) []model.GroupItem {
	sorted := make([]model.GroupItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	return sorted
}

// getLatencyWeight returns the latency influence weight (0.0–1.0) for Auto strategy scoring.
// A value of 0 means latency is ignored; 1.0 means only latency matters.
func getLatencyWeight() float64 {
	v, err := setting.GetInt(model.SettingKeyAutoStrategyLatencyWeight)
	if err != nil || v < 0 || v > 100 {
		return 0.3 // default 30% latency weight
	}
	return float64(v) / 100.0
}

// normalizeLatency maps latency in milliseconds to a 0–1 score where 1.0 = fast, 0.0 = slow.
// Uses a simple linear normalization capped at maxLatency (5000ms).
func normalizeLatency(latencyMs float64) float64 {
	if latencyMs <= 0 {
		return 1.0 // no data yet: assume fast
	}
	const maxLatency = 5000.0
	return math.Max(0, 1-latencyMs/maxLatency)
}

// sortByWeight 已删除：旧实现只做权重降序排序，权重最高的渠道吃满 100% 流量直到
// 挂掉、低权重仅做备胎。权重语义现由 Weighted.Candidates 的无放回加权随机承担
// （上游 eff51e23 的 smoothWRRSequence 未采用，理由见 Weighted 的注释）。
