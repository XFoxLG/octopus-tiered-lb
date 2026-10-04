package task

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/lingyuins/octopus/internal/utils/log"
)

// taskEntry 并发约束：
//   - interval/paused 由 mu 保护；notify（容量 1）仅作唤醒信号，循环每次醒来重读
//     mu 下最新状态，因此 Update 在循环未启动或繁忙时也不丢更新（多次合并取最新）。
//   - interval <= 0 表示内部暂停：条目保留，之后正值 Update 即恢复。
//   - RUN 在 tasksMu 下标记循环已启动；Shutdown 先等这些循环退出再等在途执行，
//     避免 wg.Add 与 wg.Wait 竞争。
type taskEntry struct {
	name       string
	fn         func()
	runOnStart bool

	mu       sync.Mutex
	interval time.Duration
	paused   bool
	notify   chan struct{}

	running atomic.Bool

	wg          sync.WaitGroup
	stopCh      chan struct{}
	loopStarted chan struct{}
	loopDone    chan struct{}
}

var (
	tasks          = make(map[string]*taskEntry)
	tasksMu        sync.RWMutex
	taskShutdownCh = make(chan struct{})
	shutdownOnce   sync.Once
)

// ResetForTest 重置任务注册表与停机状态，仅供跨包测试隔离使用；生产代码不得调用。
func ResetForTest() {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	tasks = make(map[string]*taskEntry)
	taskShutdownCh = make(chan struct{})
	shutdownOnce = sync.Once{}
}

// Shutdown 停止所有任务并等待在途执行结束，幂等。
// RUN 返回不代表各 runTask 循环已退出，必须先等全部循环退出（此后不可能再有
// wg.Add）再 Wait 在途执行。停机信号与循环启动标记由 tasksMu 串行化。
func Shutdown() {
	shutdownOnce.Do(func() {
		tasksMu.Lock()
		close(taskShutdownCh)
		entries := make([]*taskEntry, 0, len(tasks))
		for _, entry := range tasks {
			entries = append(entries, entry)
			close(entry.stopCh)
		}
		tasksMu.Unlock()
		for _, entry := range entries {
			select {
			case <-entry.loopStarted:
				<-entry.loopDone
			default:
			}
		}
		for _, entry := range entries {
			entry.wg.Wait()
		}
		log.Infof("all background tasks have been stopped")
	})
}

// Register 注册一个定时任务。runOnStart: 是否在启动时立即执行一次。
// 同名任务已存在时保留既有条目：暂停态不因重复 Register 被复活，
// 之后正值 Update 仍可恢复。
func Register(name string, interval time.Duration, runOnStart bool, fn func()) {
	tasksMu.Lock()
	defer tasksMu.Unlock()

	select {
	case <-taskShutdownCh:
		return
	default:
	}

	if entry, exists := tasks[name]; exists {
		if entry.isPaused() {
			log.Infof("task %s already registered and paused, keeping paused state", name)
		} else {
			log.Warnf("task %s already registered, skipping", name)
		}
		return
	}

	if interval <= 0 {
		log.Debugf("task %s not registered: interval is %v", name, interval)
		return
	}

	tasks[name] = &taskEntry{
		name:        name,
		interval:    interval,
		fn:          fn,
		runOnStart:  runOnStart,
		notify:      make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		loopStarted: make(chan struct{}),
		loopDone:    make(chan struct{}),
	}
	log.Debugf("task %s registered with interval %v, runOnStart: %v", name, interval, runOnStart)
}

// Update 更新任务的执行间隔。interval > 0：设置新间隔并恢复（RUN 之前调用同样
// 安全，循环启动时读取最新值）；interval <= 0：暂停任务（条目保留，在途执行
// 不受影响），之后传正值即恢复，无需重新 Register。
func Update(name string, interval time.Duration) {
	tasksMu.RLock()
	entry, exists := tasks[name]
	tasksMu.RUnlock()
	if !exists {
		log.Warnf("task %s not found", name)
		return
	}

	entry.mu.Lock()
	if interval > 0 {
		entry.interval = interval
		entry.paused = false
	} else {
		entry.paused = true
	}
	entry.mu.Unlock()

	// 非阻塞唤醒；缓冲已满说明已有待处理信号，循环醒来会读取最新状态。
	select {
	case entry.notify <- struct{}{}:
	default:
	}
	if interval > 0 {
		log.Infof("task %s interval updated to %v", name, interval)
	} else {
		log.Infof("task %s paused", name)
	}
}

func (e *taskEntry) isPaused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// RUN 启动所有已注册任务的调度循环，并阻塞直到收到停机信号。
func RUN() {
	tasksMu.Lock()
	shutdownCh := taskShutdownCh
	select {
	case <-shutdownCh:
		tasksMu.Unlock()
		return
	default:
	}
	for _, entry := range tasks {
		select {
		case <-entry.loopStarted:
			continue
		default:
		}
		close(entry.loopStarted)
		go runTask(entry)
	}
	tasksMu.Unlock()

	<-shutdownCh
}

func runTask(entry *taskEntry) {
	defer close(entry.loopDone)

	var (
		ticker    *time.Ticker
		tickEvery time.Duration
	)
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	started := false // runOnStart 只在首次进入活跃状态时执行一次
	for {
		entry.mu.Lock()
		paused := entry.paused
		interval := entry.interval
		entry.mu.Unlock()

		if paused || interval <= 0 {
			// 暂停即停表：不产生 tick，等待恢复或停机。
			if ticker != nil {
				ticker.Stop()
				ticker = nil
				tickEvery = 0
			}
			select {
			case <-entry.notify:
				continue
			case <-entry.stopCh:
				return
			}
		}

		if !started && entry.runOnStart {
			started = true
			entry.runOnce()
		}

		// 仅在间隔实际变化时重建/Reset，保持固定节拍（不每轮从当前时刻起算）。
		switch {
		case ticker == nil:
			ticker = time.NewTicker(interval)
			tickEvery = interval
		case tickEvery != interval:
			ticker.Reset(interval)
			tickEvery = interval
		}

		select {
		case <-ticker.C:
			entry.runOnce()
		case <-entry.notify:
		case <-entry.stopCh:
			return
		}
	}
}

func (entry *taskEntry) runOnce() {
	select {
	case <-entry.stopCh:
		return
	default:
	}
	if !entry.running.CompareAndSwap(false, true) {
		log.Warnf("task %s is still running, skipping overlapping run", entry.name)
		return
	}
	entry.wg.Add(1)
	go func() {
		defer entry.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("task %s panic recovered: %v", entry.name, r)
			}
		}()
		defer entry.running.Store(false)
		entry.fn()
	}()
}
