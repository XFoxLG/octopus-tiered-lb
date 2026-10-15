package balancer

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/model"
)

// DisposableChannelFunc 由 relay 层在 init 时注入，用于查询某个渠道是否为「一次性渠道」。
// 一次性渠道在路由组内绝对优先（趁未过期先用掉），由 init_hooks.go 从 channel cache 注入。
// nil 表示未启用（不做一次性优先排序），后台任务等场景不受影响。
var DisposableChannelFunc func(channelID int) bool

// Iterator 统一的负载均衡迭代器
// 内部编排：策略排序 + 粘性优先 + 决策追踪
type Iterator struct {
	mu sync.Mutex

	candidates []model.GroupItem
	index      int
	stickyIdx  int    // 粘性通道在 candidates 中的位置，-1 表示无
	modelName  string // 请求模型名（用于熔断检查）

	// 内嵌追踪
	attempts          []model.ChannelAttempt
	count             int
	attemptNumberBase int
	skipCount         int // 已保留明细的跳过/熔断记录数
	omittedSkips      int // 超出 maxSkipAttemptRecords 后未保留明细的跳过记录数
}

// SetAttemptNumberBase makes attempt identifiers request-global while each
// route round still owns an independent Iterator. It must be called before the
// iterator records its first skip or forwarded attempt.
func (it *Iterator) SetAttemptNumberBase(base int) {
	if it == nil {
		return
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.count != 0 {
		return
	}
	if base < 0 {
		base = 0
	}
	it.attemptNumberBase = base
}

// LastAttemptNumber returns the request-global decision sequence after this
// iterator's route round, including omitted skip decisions.
func (it *Iterator) LastAttemptNumber() int {
	if it == nil {
		return 0
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.attemptNumberBase + it.count
}

// maxSkipAttemptRecords 限制单个迭代器保留的跳过/熔断明细条数。跳过记录不发起
// 真实请求，异常场景下（如重试逻辑缺陷、大量渠道同时熔断）可能高频产生；
// 无上限时曾出现单条 relay log 数百 MB、写爆数据库的事故（issue #192）。
// 超限后只累计条数，Attempts() 以一条汇总记录补充说明。真实转发记录不受限
// （其数量受路由轮次 × 渠道数 × Key 重试数约束），ForwardedAttempts 语义不变。
const maxSkipAttemptRecords = 200

// appendSkipAttempt 追加一条跳过类记录，超限后只计数不存明细。
func (it *Iterator) appendSkipAttemptLocked(attempt model.ChannelAttempt) {
	if it.skipCount >= maxSkipAttemptRecords {
		it.omittedSkips++
		return
	}
	it.skipCount++
	it.attempts = append(it.attempts, attempt)
}

func (it *Iterator) appendAttempt(attempt model.ChannelAttempt) {
	if it == nil {
		return
	}
	it.mu.Lock()
	it.attempts = append(it.attempts, attempt)
	it.mu.Unlock()
}

// NewIterator 创建负载均衡迭代器
// 自动处理：策略排序 + 渠道黑名单过滤 + 粘性通道提前
// excludedChannels 为该 API Key 排除的渠道 ID 集合（issue #55），nil/空表示不排除。
// promptCacheKey（可选，v2.9.0）：客户端携带的缓存亲和键，用于把粘性粒度细化到
// 「Key × 模型 × prompt_cache_key」；缺省时退回原有「Key × 模型」行为。
func NewIterator(group model.Group, apiKeyID int, requestModel string, excludedChannels map[int]struct{}, promptCacheKey ...string) *Iterator {
	group.Items = deduplicateGroupItems(group.Items)
	b := GetBalancer(group.Mode)
	candidates := b.Candidates(group.Items)

	// 按 API Key 渠道黑名单剔除候选。在 sticky 选择之前过滤，确保被排除的渠道
	// 既不参与负载均衡，也不会作为粘性通道命中。
	if len(excludedChannels) > 0 {
		filtered := candidates[:0]
		for _, item := range candidates {
			if _, excluded := excludedChannels[item.ChannelID]; !excluded {
				filtered = append(filtered, item)
			}
		}
		candidates = filtered
	}
	// 一次性渠道绝对优先：将 disposable=true 的候选稳定地提到前面，同类内保持策略排序。
	// 优先级链：sticky > 一次性渠道 > 普通渠道（按策略）。sticky 逻辑在后面会把粘性通道
	// 移到 index 0，覆盖此排序——这是预期的（进行中的会话优先于一次性优先）。
	if DisposableChannelFunc != nil {
		sort.SliceStable(candidates, func(i, j int) bool {
			di := DisposableChannelFunc(candidates[i].ChannelID)
			dj := DisposableChannelFunc(candidates[j].ChannelID)
			return di && !dj
		})
	}

	stickyIdx := -1
	if group.SessionKeepTime > 0 {
		stickyTTL := time.Duration(group.SessionKeepTime) * time.Second
		cacheKey := ""
		if len(promptCacheKey) > 0 {
			cacheKey = promptCacheKey[0]
		}
		if sticky := GetStickyFor(apiKeyID, requestModel, cacheKey, stickyTTL); sticky != nil {
			for i, item := range candidates {
				if item.ChannelID == sticky.ChannelID {
					if i > 0 {
						// 将粘性通道移到最前面
						stickyItem := candidates[i]
						copy(candidates[1:i+1], candidates[0:i])
						candidates[0] = stickyItem
					}
					stickyIdx = 0
					break
				}
			}
		}
	}

	return &Iterator{
		candidates: candidates,
		index:      -1,
		stickyIdx:  stickyIdx,
		modelName:  requestModel,
	}
}

// deduplicateGroupItems protects the routing loop from malformed/imported
// groups that contain the same channel/model pair more than once. Different
// model mappings on the same channel remain distinct candidates because they
// may require different upstream model names.
func deduplicateGroupItems(items []model.GroupItem) []model.GroupItem {
	if len(items) < 2 {
		return items
	}

	seen := make(map[string]int, len(items))
	result := make([]model.GroupItem, 0, len(items))
	for _, item := range items {
		key := fmt.Sprintf("%d\x00%s", item.ChannelID, strings.TrimSpace(item.ModelName))
		index, exists := seen[key]
		if !exists {
			seen[key] = len(result)
			result = append(result, item)
			continue
		}

		// Preserve the strongest effective configuration when duplicate rows
		// came from an import: maximum weight, then minimum priority.
		if item.Weight > result[index].Weight {
			result[index].Weight = item.Weight
		}
		if result[index].Priority == 0 || (item.Priority > 0 && item.Priority < result[index].Priority) {
			result[index].Priority = item.Priority
		}
	}
	return result
}

// Next 移动到下一个候选，返回 false 表示遍历完成
func (it *Iterator) Next() bool {
	if it == nil {
		return false
	}
	it.mu.Lock()
	it.index++
	ok := it.index < len(it.candidates)
	it.mu.Unlock()
	return ok
}

// Item 返回当前候选的 GroupItem
func (it *Iterator) Item() model.GroupItem {
	if it == nil {
		return model.GroupItem{}
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.index < 0 || it.index >= len(it.candidates) {
		return model.GroupItem{}
	}
	return it.candidates[it.index]
}

func (it *Iterator) isStickyLocked() bool {
	return it.stickyIdx >= 0 && it.index == it.stickyIdx
}

// IsSticky 当前候选是否为粘性通道
func (it *Iterator) IsSticky() bool {
	if it == nil {
		return false
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.isStickyLocked()
}

// Len 返回候选列表长度
func (it *Iterator) Len() int {
	if it == nil {
		return 0
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return len(it.candidates)
}

// Index 返回当前迭代位置（0-based）
func (it *Iterator) Index() int {
	if it == nil {
		return -1
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.index
}

// Skip 记录当前通道被跳过（通道禁用、无Key、类型不兼容等）
func (it *Iterator) Skip(channelID, channelKeyID int, channelName, msg string) {
	if it == nil {
		return
	}
	it.mu.Lock()
	it.count++
	attemptModel := it.currentModelNameLocked()
	it.appendSkipAttemptLocked(model.ChannelAttempt{
		ChannelID:    channelID,
		ChannelKeyID: channelKeyID,
		ChannelName:  channelName,
		ModelName:    attemptModel,
		AttemptNum:   it.attemptNumberBase + it.count,
		Status:       model.AttemptSkipped,
		Sticky:       it.isStickyLocked(),
		Msg:          msg,
	})
	it.mu.Unlock()
	// v2.9.0：把最近一次跳过原因写进渠道×模型画像（分析中心「最近决策原因」）。
	// 候选条目没有模型覆盖时退回请求级模型名（与统计键口径一致）。
	if strings.TrimSpace(attemptModel) == "" {
		attemptModel = it.modelName
	}
	RecordAutoDecisionReason(channelID, attemptModel, msg)
}

// SkipCircuitBreak 检查熔断状态，若已熔断自动记录（含剩余冷却时间）并返回 true
func (it *Iterator) SkipCircuitBreak(channelID, channelKeyID int, channelName, modelName string) bool {
	tripped, remaining := IsTripped(channelID, channelKeyID, modelName)
	if !tripped {
		return false
	}
	msg := "circuit breaker tripped"
	if remaining > 0 {
		msg = fmt.Sprintf("circuit breaker tripped, remaining cooldown: %ds", int(remaining.Seconds()))
	}
	if it == nil {
		return true
	}
	it.mu.Lock()
	it.count++
	it.appendSkipAttemptLocked(model.ChannelAttempt{
		ChannelID:    channelID,
		ChannelKeyID: channelKeyID,
		ChannelName:  channelName,
		ModelName:    modelName,
		AttemptNum:   it.attemptNumberBase + it.count,
		Status:       model.AttemptCircuitBreak,
		Sticky:       it.isStickyLocked(),
		Msg:          msg,
	})
	it.mu.Unlock()
	// v2.9.0：熔断跳过与普通 Skip 同口径写入渠道×模型画像的「最近决策原因」。
	// 条目模型为空时退回请求级模型名（与统计键口径一致）。
	attemptModel := strings.TrimSpace(modelName)
	if attemptModel == "" {
		attemptModel = it.modelName
	}
	RecordAutoDecisionReason(channelID, attemptModel, msg)
	return true
}

// StartAttempt 开始一次真实转发尝试，返回 Span 用于记录结果
func (it *Iterator) StartAttempt(channelID, channelKeyID int, channelName, modelName string) *AttemptSpan {
	if it == nil {
		return nil
	}
	it.mu.Lock()
	it.count++
	span := &AttemptSpan{
		attempt: model.ChannelAttempt{
			ChannelID:    channelID,
			ChannelKeyID: channelKeyID,
			ChannelName:  channelName,
			ModelName:    modelName,
			AttemptNum:   it.attemptNumberBase + it.count,
			Sticky:       it.isStickyLocked(),
		},
		startTime: time.Now(),
		iter:      it,
	}
	it.mu.Unlock()
	return span
}

// Attempts 返回所有决策记录（交给日志模块持久化）。
// 跳过明细超限时追加一条汇总记录说明省略数量，保证日志读者知道有截断。
func (it *Iterator) Attempts() []model.ChannelAttempt {
	if it == nil {
		return nil
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	out := make([]model.ChannelAttempt, len(it.attempts), len(it.attempts)+1)
	copy(out, it.attempts)
	if it.omittedSkips == 0 {
		return out
	}
	return append(out, model.ChannelAttempt{
		AttemptNum: it.attemptNumberBase + it.count,
		Status:     model.AttemptSkipped,
		Msg:        fmt.Sprintf("%d skip/circuit-break records omitted (cap %d per route round)", it.omittedSkips, maxSkipAttemptRecords),
	})
}

// ForwardedAttempts 返回真实发往上游的尝试次数，不包含跳过和熔断拒绝。
func (it *Iterator) ForwardedAttempts() int {
	if it == nil {
		return 0
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	count := 0
	for _, attempt := range it.attempts {
		if attempt.Status == model.AttemptSkipped || attempt.Status == model.AttemptCircuitBreak {
			continue
		}
		count++
	}
	return count
}

// AttemptSpan 管理单次通道尝试的生命周期（计时、状态、结果）
type AttemptSpan struct {
	mu        sync.Mutex
	attempt   model.ChannelAttempt
	startTime time.Time
	iter      *Iterator
	ended     bool
}

// End 结束尝试：设置状态，自动计算耗时，追加到 Iterator
func (s *AttemptSpan) End(status model.AttemptStatus, statusCode int, msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.attempt.Status = status
	s.attempt.HTTPStatus = statusCode
	s.attempt.Duration = int(time.Since(s.startTime).Milliseconds())
	s.attempt.Msg = msg
	attempt := s.attempt
	it := s.iter
	s.mu.Unlock()
	if it != nil {
		it.appendAttempt(attempt)
	}
}

// SetAdapterType 设置适配器类型（response, chat, anthropic 等）
func (s *AttemptSpan) SetAdapterType(adapterType string) {
	s.attempt.AdapterType = adapterType
}

// AttemptNumber returns the stable decision-sequence number assigned before
// the request is sent. Boundary logging uses it to pair wire data with the
// corresponding ChannelAttempt row.
func (s *AttemptSpan) AttemptNumber() int {
	if s == nil {
		return 0
	}
	return s.attempt.AttemptNum
}

// Duration 返回从开始到现在的耗时
func (s *AttemptSpan) StartedAt() time.Time { return s.startTime }

func (s *AttemptSpan) Duration() time.Duration {
	return time.Since(s.startTime)
}

// PeekNext keeps candidate order unchanged until a backup is admitted. The
// coordinator consumes it with Next only after reserving resources.
func (it *Iterator) PeekNext() (model.GroupItem, bool) {
	if it == nil {
		return model.GroupItem{}, false
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.index+1 >= len(it.candidates) {
		return model.GroupItem{}, false
	}
	return it.candidates[it.index+1], true
}
func (it *Iterator) HasSticky() bool {
	if it == nil {
		return false
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.stickyIdx >= 0
}

func (it *Iterator) RequestModel() string {
	if it == nil {
		return ""
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.modelName
}

func (it *Iterator) currentModelNameLocked() string {
	if it.index < 0 || it.index >= len(it.candidates) {
		return ""
	}
	return it.candidates[it.index].ModelName
}
