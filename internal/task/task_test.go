package task

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// resetRegistry 重建任务注册表与停机状态，使每个测试从干净的包级全局状态开始，
// 互不污染（tasks / taskShutdownCh / shutdownOnce 均为包级全局）。
func resetRegistry() {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks = make(map[string]*taskEntry)
	taskShutdownCh = make(chan struct{})
	shutdownOnce = sync.Once{}
}

// setupIsolatedTasks 在每个测试开始时清空注册表，并在结束时停掉本测试启动的
// 调度循环与在途执行，避免泄漏到后续测试。
func setupIsolatedTasks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		Shutdown()
		resetRegistry()
	})
	resetRegistry()
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// TestSettingIntervalFallsBackForInvalidStoredValues 锁住「设置读取失败/非法值
// 不导致任务静默不注册」：回退到 DefaultSettings 登记的默认值；合法值按单位换算。
func TestSettingIntervalFallsBackForInvalidStoredValues(t *testing.T) {
	key := model.SettingKeyStatsSaveInterval
	cache := setting.GetCache()
	previous, existed := cache.Get(key)
	t.Cleanup(func() {
		if existed {
			cache.Set(key, previous)
		} else {
			cache.Del(key)
		}
	})
	for _, value := range []string{"", "abc", "0", "-1", "153722868"} {
		cache.Set(key, value)
		if got := settingInterval(key, time.Minute, time.Hour); got != 10*time.Minute {
			t.Fatalf("value %q produced %v instead of the registered default", value, got)
		}
	}
	cache.Set(key, "25")
	if got := settingInterval(key, time.Minute, time.Hour); got != 25*time.Minute {
		t.Fatalf("valid interval produced %v", got)
	}
}

// TestUpdateBeforeRUNTakesEffect 锁住「启动前更新不丢失」：旧实现用无缓冲
// updateCh + select default，在 RUN 之前调用 Update 必然走 default 分支被丢弃。
// 新实现把目标间隔记录在 mu 保护的字段里，循环启动时读取。
func TestUpdateBeforeRUNTakesEffect(t *testing.T) {
	setupIsolatedTasks(t)

	var calls atomic.Int32
	Register("t1", time.Hour, false, func() { calls.Add(1) })
	Update("t1", 20*time.Millisecond)

	go RUN()
	waitFor(t, func() bool { return calls.Load() >= 2 }, 3*time.Second)
}

// TestConsecutiveUpdatesKeepLatest 锁住「连续更新保留最新值」：最后一次 Update
// 决定实际间隔，先前的短间隔不再生效。
func TestConsecutiveUpdatesKeepLatest(t *testing.T) {
	setupIsolatedTasks(t)

	var calls atomic.Int32
	Register("t1", time.Hour, false, func() { calls.Add(1) })
	go RUN()

	Update("t1", 30*time.Millisecond)
	waitFor(t, func() bool { return calls.Load() >= 1 }, 3*time.Second)

	// 最新值是 10s：若旧实现按到达顺序逐个生效（或更新丢失停在 30ms），
	// 后续观察窗口内会继续累积执行。
	Update("t1", 10*time.Second)
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() != 1 {
			t.Fatalf("task kept executing after interval updated to 10s: calls=%d", calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPauseAndResume 锁住「非正间隔 = 内部暂停（条目保留），正值恢复」。
// 旧实现 Update(0) 直接删除条目并关闭 stopCh，之后无法用正值恢复。
func TestPauseAndResume(t *testing.T) {
	setupIsolatedTasks(t)

	var calls atomic.Int32
	Register("t1", 30*time.Millisecond, false, func() { calls.Add(1) })
	go RUN()
	waitFor(t, func() bool { return calls.Load() >= 1 }, 3*time.Second)

	Update("t1", 0) // 暂停
	// 先给一个宽限窗：让与暂停并发的那次 tick 落地、循环观察到暂停态。
	time.Sleep(250 * time.Millisecond)
	pausedAt := calls.Load()
	// 观察窗：30ms 间隔若未暂停，250ms 内必然继续累积。
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() != pausedAt {
			t.Fatalf("task kept executing after pause: before=%d after=%d", pausedAt, calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	Update("t1", 30*time.Millisecond) // 恢复
	waitFor(t, func() bool { return calls.Load() > pausedAt }, 3*time.Second)
}

// TestPauseBeforeRunSuppressesRunOnStart 锁住「启动前暂停同样正确」：RUN 之前
// Update(0) 的任务，启动后不补跑 runOnStart，恢复正值后正常调度。
func TestPauseBeforeRunSuppressesRunOnStart(t *testing.T) {
	setupIsolatedTasks(t)

	var calls atomic.Int32
	Register("t1", 30*time.Millisecond, true, func() { calls.Add(1) })
	Update("t1", 0)

	go RUN()
	time.Sleep(250 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("paused task must not run on start, calls=%d", calls.Load())
	}

	Update("t1", 30*time.Millisecond)
	waitFor(t, func() bool { return calls.Load() >= 1 }, 3*time.Second)
}

// TestRegisterKeepsPausedEntry 锁住「Register 保留暂停条目以恢复旧配置」：
// 暂停后重复 Register 不得复活任务；后续正值 Update 仍能恢复。
func TestRegisterKeepsPausedEntry(t *testing.T) {
	setupIsolatedTasks(t)

	var calls atomic.Int32
	Register("t1", 30*time.Millisecond, false, func() { calls.Add(1) })
	Update("t1", 0) // 暂停
	Register("t1", time.Minute, false, func() { calls.Add(1) })

	go RUN()
	time.Sleep(250 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("re-Register must not resume a paused task, calls=%d", calls.Load())
	}

	Update("t1", 30*time.Millisecond)
	waitFor(t, func() bool { return calls.Load() >= 1 }, 3*time.Second)
}

// TestNoOverlappingExecution 锁住「同一任务不可重叠」：任务体耗时横跨多个
// tick 时，重叠的 tick 必须被跳过，并发度恒为 1。
func TestNoOverlappingExecution(t *testing.T) {
	setupIsolatedTasks(t)

	var (
		calls      atomic.Int32
		concurrent atomic.Int32
		maxSeen    atomic.Int32
	)
	Register("t1", 10*time.Millisecond, false, func() {
		cur := concurrent.Add(1)
		for {
			old := maxSeen.Load()
			if cur <= old || maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond) // 横跨多个 tick
		concurrent.Add(-1)
		calls.Add(1)
	})
	go RUN()

	waitFor(t, func() bool { return calls.Load() >= 3 }, 5*time.Second)
	if maxSeen.Load() != 1 {
		t.Fatalf("task executions overlapped: max concurrency=%d", maxSeen.Load())
	}
}

// TestShutdownWaitsForInFlightExecution 锁住停机时序：Shutdown 必须等在途执行
// 完成后才返回；且幂等（重复调用不得 double-close panic）。
func TestShutdownWaitsForInFlightExecution(t *testing.T) {
	setupIsolatedTasks(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool
	Register("t1", time.Hour, true, func() {
		close(started)
		<-release
		finished.Store(true)
	})
	go RUN()
	<-started

	done := make(chan struct{})
	go func() {
		Shutdown()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Shutdown returned while a task execution was still in flight")
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return after the in-flight execution finished")
	}
	if !finished.Load() {
		t.Fatal("in-flight execution did not run to completion")
	}

	// 幂等：重复 Shutdown 不得 panic（旧实现会 double-close taskShutdownCh）。
	Shutdown()
}

// TestShutdownBeforeRUNPreventsExecution 锁住「先停机后启动不执行」。
func TestShutdownBeforeRUNPreventsExecution(t *testing.T) {
	setupIsolatedTasks(t)
	var calls atomic.Int32
	Register("late", time.Nanosecond, true, func() { calls.Add(1) })
	Shutdown()
	RUN()
	if calls.Load() != 0 {
		t.Fatal("task started after shutdown")
	}
}

// TestConcurrentRUNAndShutdown 覆盖 RUN 与 Shutdown 并发：Shutdown 返回前
// 调度循环必须已退出，且不得因 wg.Add/Wait 竞争 panic。
func TestConcurrentRUNAndShutdown(t *testing.T) {
	for i := 0; i < 25; i++ {
		t.Run("startup", func(t *testing.T) {
			setupIsolatedTasks(t)
			var calls atomic.Int32
			Register("startup", time.Nanosecond, true, func() { calls.Add(1) })
			done := make(chan struct{})
			go func() {
				RUN()
				close(done)
			}()
			Shutdown()
			<-done
			entry := tasks["startup"]
			select {
			case <-entry.loopStarted:
				select {
				case <-entry.loopDone:
				default:
					t.Fatal("shutdown returned before the scheduler loop stopped")
				}
			default:
			}
		})
	}
}

// TestShutdownWithoutRunDoesNotHang：只 Register 不 RUN 时 Shutdown 也必须返回
// （循环从未启动，loopStarted 永不关闭，Shutdown 不得在此挂起）。
func TestShutdownWithoutRunDoesNotHang(t *testing.T) {
	setupIsolatedTasks(t)

	Register("t1", time.Hour, false, func() {})
	done := make(chan struct{})
	go func() {
		Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown hung when the scheduler loops were never started")
	}
}
