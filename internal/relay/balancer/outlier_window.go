// Package balancer 内的离群窗口构件：按渠道（channelID）维护进程内滚动成败窗口，
// 为渠道离群（持续失败）提供可查询的证据（Seller outlierwindow 移植）。
//
// 设计要点（与 Seller 一致）：
//   - 数据面（relay）在每次真实请求最终结果点调用 OutlierReport，纳秒级、非阻塞；
//   - 运维面按需调用 OutlierEvaluate 获取窗口统计 + 离群初判；
//   - 纯内存、重启清空，与熔断器（circuit.go）同构；默认开启记录，
//     SetOutlierReportEnabled(false) 可关闭热路径写入。
package balancer

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/lingyuins/octopus/internal/utils/log"
)

// outlierPhysicalCap 是环形缓冲的物理容量（编译期常量）。
// OutlierConfig.Capacity 仅作「评估时取最近 N 条」的逻辑上限，不能超过该值。
const outlierPhysicalCap = 20

type outlierSample struct {
	at      time.Time
	success bool
}

// outlierRingWindow 单个渠道的定长环形缓冲。零堆分配，原地加锁修改。
type outlierRingWindow struct {
	mu       sync.Mutex
	buf      [outlierPhysicalCap]outlierSample
	size     int       // 已填充数 (<=outlierPhysicalCap)
	next     int       // 下一写入位
	lastSeen time.Time // 最近 Report 时间，用于内存回收
}

// OutlierConfig 离群判定阈值，由调用方按需注入。
type OutlierConfig struct {
	Capacity    int           // 评估时取最近 N 条（≤outlierPhysicalCap）
	TimeWindow  time.Duration // T：样本过期时长
	MinSamples  int           // 样本不足直接 PASS
	FailRate    float64       // 失败率阈值 (0,1]
	ConsecFails int           // 连续失败阈值
}

var defaultOutlierConfig = OutlierConfig{
	Capacity:    outlierPhysicalCap,
	TimeWindow:  10 * time.Minute,
	MinSamples:  8,
	FailRate:    0.85,
	ConsecFails: 10,
}

var (
	outlierStore        sync.Map // key: int(channelID) -> *outlierRingWindow
	outlierConfigPtr    atomic.Pointer[OutlierConfig]
	outlierReportEnable atomic.Bool
)

func init() {
	c := defaultOutlierConfig
	outlierConfigPtr.Store(&c)
	outlierReportEnable.Store(true)
}

// SetOutlierReportEnabled 设置数据面记录开关（关闭时停止每请求写入，热路径开销归零）。
func SetOutlierReportEnabled(enabled bool) {
	outlierReportEnable.Store(enabled)
}

func currentOutlierConfig() OutlierConfig {
	if c := outlierConfigPtr.Load(); c != nil {
		return *c
	}
	return defaultOutlierConfig
}

// ConfigureOutlierWindow 注入离群判定阈值（无锁热更）。非法值回退默认；Capacity 超上限封顶。
func ConfigureOutlierWindow(c OutlierConfig) {
	if c.Capacity <= 0 || c.Capacity > outlierPhysicalCap {
		c.Capacity = outlierPhysicalCap
	}
	if c.TimeWindow <= 0 {
		c.TimeWindow = defaultOutlierConfig.TimeWindow
	}
	if c.MinSamples <= 0 {
		c.MinSamples = defaultOutlierConfig.MinSamples
	}
	if c.FailRate <= 0 || c.FailRate > 1 {
		c.FailRate = defaultOutlierConfig.FailRate
	}
	if c.ConsecFails <= 0 {
		c.ConsecFails = defaultOutlierConfig.ConsecFails
	}
	outlierConfigPtr.Store(&c)
}

// OutlierPhysicalCap 返回环形缓冲的物理容量，供调用方校验 Capacity 上限。
func OutlierPhysicalCap() int { return outlierPhysicalCap }

func getOrCreateOutlierWindow(channelID int) *outlierRingWindow {
	if v, ok := outlierStore.Load(channelID); ok {
		return v.(*outlierRingWindow)
	}
	w := &outlierRingWindow{}
	actual, _ := outlierStore.LoadOrStore(channelID, w)
	return actual.(*outlierRingWindow)
}

// OutlierReport 数据面每次真实请求最终结果调用。非阻塞、best-effort，内部 recover 兜底绝不冒泡。
// statusCode 当前不参与判定（只看成败序列），保留入参供未来扩展。
func OutlierReport(channelID int, success bool, statusCode int, now time.Time) {
	if !outlierReportEnable.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("outlier window report panic: %v", r)
		}
	}()
	_ = statusCode
	w := getOrCreateOutlierWindow(channelID)
	w.mu.Lock()
	w.buf[w.next] = outlierSample{at: now, success: success}
	w.next = (w.next + 1) % outlierPhysicalCap
	if w.size < outlierPhysicalCap {
		w.size++
	}
	w.lastSeen = now
	w.mu.Unlock()
}

// OutlierWindowStats 窗口统计 + 离群初判结果。
type OutlierWindowStats struct {
	Samples          int       // 窗口内有效（未过期、取最近 Capacity）样本数
	Failures         int       // 其中失败数
	FailureRate      float64   // Failures/Samples；Samples=0 时为 0
	ConsecutiveFails int       // 从最新往回数的连续失败数
	LastSuccessAt    time.Time // 有效样本中最近一次成功；无则零值
	LastSampleAt     time.Time // 有效样本中最近一次的时间
	Candidate        bool      // 离群初判：true=持续离群候选
}

// OutlierEvaluate 返回窗口统计 + 离群判定。惰性过期裁剪，不清窗。
func OutlierEvaluate(channelID int, now time.Time) OutlierWindowStats {
	v, ok := outlierStore.Load(channelID)
	if !ok {
		return OutlierWindowStats{}
	}
	return v.(*outlierRingWindow).evaluate(now, currentOutlierConfig())
}

func (w *outlierRingWindow) orderedLocked() []outlierSample {
	if w.size == 0 {
		return nil
	}
	start := 0
	if w.size == outlierPhysicalCap {
		start = w.next
	}
	out := make([]outlierSample, 0, w.size)
	for i := 0; i < w.size; i++ {
		out = append(out, w.buf[(start+i)%outlierPhysicalCap])
	}
	return out
}

func (w *outlierRingWindow) evaluate(now time.Time, c OutlierConfig) OutlierWindowStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	ordered := w.orderedLocked()
	cutoff := now.Add(-c.TimeWindow)
	valid := make([]outlierSample, 0, len(ordered))
	for _, s := range ordered {
		if s.at.After(cutoff) {
			valid = append(valid, s)
		}
	}
	if len(valid) > c.Capacity {
		valid = valid[len(valid)-c.Capacity:]
	}

	var st OutlierWindowStats
	st.Samples = len(valid)
	if st.Samples == 0 {
		return st
	}
	for _, s := range valid {
		if s.success {
			st.LastSuccessAt = s.at
		} else {
			st.Failures++
		}
	}
	st.LastSampleAt = valid[len(valid)-1].at
	st.FailureRate = float64(st.Failures) / float64(st.Samples)
	for i := len(valid) - 1; i >= 0; i-- {
		if valid[i].success {
			break
		}
		st.ConsecutiveFails++
	}

	if st.Samples < c.MinSamples {
		return st
	}
	if st.FailureRate < c.FailRate {
		return st
	}
	noSuccess := st.LastSuccessAt.IsZero()
	st.Candidate = st.ConsecutiveFails >= c.ConsecFails || noSuccess
	return st
}

// ClearOutlierWindow 清空指定渠道窗口（恢复后调用，重新积累证据）。
func ClearOutlierWindow(channelID int) {
	outlierStore.Delete(channelID)
}

// ReapOutlierWindows 回收 lastSeen 早于 now-ttl 的窗口（已删除/长期无流量渠道），返回回收数。
func ReapOutlierWindows(now time.Time, ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	cutoff := now.Add(-ttl)
	reaped := 0
	outlierStore.Range(func(key, value any) bool {
		w, ok := value.(*outlierRingWindow)
		if !ok {
			return true
		}
		w.mu.Lock()
		if w.lastSeen.Before(cutoff) {
			outlierStore.Delete(key)
			reaped++
		}
		w.mu.Unlock()
		return true
	})
	return reaped
}
