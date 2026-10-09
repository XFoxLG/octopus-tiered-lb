package balancer

import (
	"math"
	"math/rand"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// nextAutoCandidatesFloat64 返回 [0,1) 的伪随机数，供 Auto 策略随机化使用
// （同分桶洗牌 softmax 采样）。独立成变量便于测试注入确定性随机源。
var nextAutoCandidatesFloat64 = rand.Float64

// autoPostSortShuffle 在 Auto 利用阶段排序完成后做两级随机化（B2）：
//  1. 同分桶打散：读 SettingKeyAutoStrategyBucketTolerance（默认 5 评分点，0=禁用），
//     把评分差在容差内的相邻候选划为同分桶，桶内 rand.Shuffle 洗牌，消除确定性轮流垄断。
//  2. softmax 探索：读 SettingKeyAutoStrategyExploreRate（默认 0=保持旧行为），
//     >0 时以 rate/100 概率对已排序候选整体做一次 softmax 温度采样重排（不放回抽取）。
//
// scored 与 result 必须同长度且 result 已按 scored 顺序填充；本函数就地重排 result。
// 探索阶段（存在 explored=false 候选）直接返回，保持确定性排序不动（任务 B2.d）。
func autoPostSortShuffle(scored []autoScoredItem, result []model.GroupItem) {
	if len(result) == 0 || len(scored) != len(result) {
		return
	}
	if !autoAllExplored(scored) {
		return
	}

	bucketTolerance := getAutoBucketTolerance()
	if bucketTolerance > 0 {
		autoShuffleScoreBuckets(scored, result, bucketTolerance)
	}

	exploreRate := getAutoExploreRate()
	if exploreRate > 0 && nextAutoCandidatesFloat64() < exploreRate {
		autoSoftmaxShuffle(scored, result)
	}
}

// autoAllExplored 判断是否全部候选都进入利用阶段（explored=true）。
// 只要有一个候选处于探索阶段（样本不足），整个排序保持确定性，不做随机化。
func autoAllExplored(scored []autoScoredItem) bool {
	for i := range scored {
		if !scored[i].explored {
			return false
		}
	}
	return true
}

// autoShuffleScoreBuckets 按容差把相邻同分候选分桶洗牌。
// 分桶规则：从当前最高分开始，桶下界 = 桶首分数 - tolerance；依次向后扫描，
// 分数 >= 桶下界的相邻候选进同一桶（「分数差在容差内」的链式相邻判定），
// 遇到低于下界的候选则开新桶。桶内 rand.Shuffle 打散。
// 注意：scored 必须已按分数降序排好（调用方 Auto.Candidates 的 sort 保证）；
// 洗牌只动 result 的桶内区间，不影响 scored 本身。
func autoShuffleScoreBuckets(scored []autoScoredItem, result []model.GroupItem, tolerance float64) {
	n := len(scored)
	start := 0
	for start < n {
		bucketFloor := scored[start].score - tolerance
		end := start + 1
		for end < n && scored[end].score >= bucketFloor {
			end++
		}
		if end-start > 1 {
			lo, hi := start, end
			rand.Shuffle(hi-lo, func(i, j int) {
				result[lo+i], result[lo+j] = result[lo+j], result[lo+i]
			})
		}
		start = end
	}
}

// autoSoftmaxShuffle 对整个候选列表做 softmax 温度采样重排（不放回抽取）。
// 分数先钳到 [0,1]（评分本身在 [0,1]，全熔断 -Inf 落 0），再以 max 为基准算
// 损失 d_i = maxScore - s_i，权重 w_i = exp(-d_i / temperature)（temperature=1）。
// 每轮按剩余权重比例不放回抽取一个候选，放入新排列当前位置。
// 全同分时 w_i 全相等 → 均匀随机排列；分差大时高分候选大概率仍排前。
func autoSoftmaxShuffle(scored []autoScoredItem, result []model.GroupItem) {
	n := len(scored)
	if n <= 1 {
		return
	}

	maxScore := math.Inf(-1)
	for i := range scored {
		if scored[i].score > maxScore {
			maxScore = scored[i].score
		}
	}
	if math.IsInf(maxScore, -1) {
		maxScore = 0 // 全部 -Inf：钳位后全 0，均匀随机
	}

	const temperature = 1.0
	weights := make([]float64, n)
	total := 0.0
	for i := range scored {
		s := scored[i].score
		if math.IsInf(s, -1) {
			// 全熔断渠道保持 -Inf：w = exp(-Inf) = 0，健康渠道存在时不参与竞争
			// （issue #133 全熔断只降权不剔除，排序兜底由 weight/priority 决定）。
		} else if s < 0 {
			s = 0
		} else if s > 1 {
			s = 1
		}
		w := math.Exp(-(maxScore - s) / temperature)
		weights[i] = w
		total += w
	}
	if total <= 0 || math.IsInf(total, 1) || math.IsNaN(total) {
		return
	}

	// 不放回加权抽取：remaining 标记候选是否已被抽走。
	taken := make([]bool, n)
	for pos := 0; pos < n; pos++ {
		sum := 0.0
		for i := range taken {
			if !taken[i] {
				sum += weights[i]
			}
		}
		if sum <= 0 {
			// 剩余权重全为 0（分数极差的候选）：均匀随机收尾。
			idx := make([]int, 0, n-pos)
			for i := range taken {
				if !taken[i] {
					idx = append(idx, i)
				}
			}
			rand.Shuffle(len(idx), func(i, j int) {
				result[pos+i], result[pos+j] = result[pos+j], result[pos+i]
			})
			return
		}
		target := nextAutoCandidatesFloat64() * sum
		pick := -1
		acc := 0.0
		for i := range taken {
			if taken[i] {
				continue
			}
			acc += weights[i]
			if acc >= target {
				pick = i
				break
			}
		}
		if pick < 0 {
			// 浮点误差兜底：取最后一个未抽走的候选。
			for i := n - 1; i >= 0; i-- {
				if !taken[i] {
					pick = i
					break
				}
			}
		}
		if pick < 0 {
			return
		}
		taken[pick] = true
		result[pos] = scored[pick].item
	}
}

// getAutoBucketTolerance 返回同分桶容差。设置项语义是 0-100 评分点（见
// model/setting.go 注释），Auto 评分是 [0,1] 浮点，因此换算为 v/100 后参与比较：
// 容差 5 评分点 = 0.05 的评分差。读取失败/非法值回退默认 5（即 0.05）。
func getAutoBucketTolerance() float64 {
	v, err := setting.GetInt(model.SettingKeyAutoStrategyBucketTolerance)
	if err != nil || v < 0 || v > 100 {
		return 0.05
	}
	return float64(v) / 100.0
}

// getAutoExploreRate 返回探索概率（0-100）。读取失败/非法值回退默认 0
// （0 = 完全保持旧行为，不引入任何随机性）。
func getAutoExploreRate() float64 {
	v, err := setting.GetInt(model.SettingKeyAutoStrategyExploreRate)
	if err != nil || v < 0 || v > 100 {
		return 0
	}
	return float64(v) / 100.0
}

// getAutoTTFTWeight 返回 TTFT 评分权重（0-100）。0=禁用（默认），
// >0 时 Auto 利用阶段的延迟评分输入优先使用 TTFT EMA（无样本回退总延迟）。
func getAutoTTFTWeight() float64 {
	v, err := setting.GetInt(model.SettingKeyAutoStrategyTTFTWeight)
	if err != nil || v < 0 || v > 100 {
		return 0
	}
	return float64(v) / 100.0
}

// getAutoPriceWeight 返回成本评分权重（0-100）。0=禁用（默认），
// >0 且 AutoPriceFunc 已注入且该模型有价格数据时，评分混合成本因子。
func getAutoPriceWeight() float64 {
	v, err := setting.GetInt(model.SettingKeyAutoStrategyPriceWeight)
	if err != nil || v < 0 || v > 100 {
		return 0
	}
	return float64(v) / 100.0
}
