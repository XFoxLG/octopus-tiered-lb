package snowflake

import (
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ID 位布局：timestamp(毫秒) | nodeID | sequence。
//
// 背景：此前 GenerateID 返回裸毫秒时间戳，同一毫秒内的第二次调用只是把内部计数器
// 自增（漂到未来的时间戳）。单进程下不重复，但多个进程共用一个数据库时，两边各自
// 维护计数器，同毫秒写入必然撞同一个 ID：error_logs 走裸 Create 会抛主键冲突并被
// 降级为 warn 日志，relay_logs 走 OnConflict DoNothing 则静默丢弃整条日志。
// 加入节点位后，不同实例的 ID 空间天然不重叠。
//
// 量级安全：当前毫秒时间戳约 1.77e12，左移 16 位约 1.16e17，远小于 int64 上限
// 9.22e18（溢出要到公元 6429 年）。单调性也保持：新 ID 恒大于旧的裸毫秒 ID，
// 依赖 ORDER BY id 的分页与备份导出语义不变。
const (
	sequenceBits = 8
	nodeBits     = 8

	maxSequence = (1 << sequenceBits) - 1
	MaxNodeID   = (1 << nodeBits) - 1

	nodeShift      = sequenceBits
	timestampShift = sequenceBits + nodeBits
)

const (
	// NodeIDEnvVar 显式指定节点号（0-255）。共用数据库的多实例部署应当显式设置，
	// 这是唯一能保证节点号互不相同的方式；哈希回退只能降低碰撞概率而非消除。
	NodeIDEnvVar = "OCTOPUS_NODE_ID"
	// renderServiceIDEnvVar 在 Render 上稳定标识一个服务，重启与重新部署都不变
	// （区别于每个实例都不同的 RENDER_INSTANCE_ID，后者会让重启后 ID 空间漂移）。
	renderServiceIDEnvVar = "RENDER_SERVICE_ID"
)

var (
	sfMutex    sync.Mutex
	sfLastTime int64
	sfSequence int64

	nodeIDOnce  sync.Once
	nodeIDValue int64
)

// NodeID 返回当前实例的节点号。优先取显式配置，其次由 RENDER_SERVICE_ID 稳定哈希
// 得到，都不存在时为 0（单实例部署，行为与改造前一致）。
func NodeID() int64 {
	nodeIDOnce.Do(func() { nodeIDValue = resolveNodeID() })
	return nodeIDValue
}

func resolveNodeID() int64 {
	if raw := strings.TrimSpace(os.Getenv(NodeIDEnvVar)); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 && parsed <= MaxNodeID {
			return parsed
		}
	}
	if serviceID := strings.TrimSpace(os.Getenv(renderServiceIDEnvVar)); serviceID != "" {
		// FNV-1a 而非 maphash：后者每进程加盐，重启后同一服务会换节点号。
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte(serviceID))
		return int64(hasher.Sum32() % (MaxNodeID + 1))
	}
	return 0
}

// GenerateID 生成单调递增且跨实例唯一的 ID。
// 同一毫秒内最多发放 256 个（每节点），耗尽后等待下一毫秒。
func GenerateID() int64 {
	node := NodeID()

	sfMutex.Lock()
	defer sfMutex.Unlock()

	now := time.Now().UnixMilli()
	// 时钟回拨时沿用上一次的毫秒数：宁可继续消耗当前毫秒的序列，也不能回到已经
	// 发放过的时间段重新计数，否则会与已写入的 ID 重复。
	if now < sfLastTime {
		now = sfLastTime
	}

	if now == sfLastTime {
		sfSequence++
		if sfSequence > maxSequence {
			// 序列耗尽必须换毫秒，不能让它溢出到 nodeID 位段（那会伪装成别的节点）。
			now = waitNextMilli(sfLastTime)
			sfSequence = 0
		}
	} else {
		sfSequence = 0
	}
	sfLastTime = now

	return (now << timestampShift) | (node << nodeShift) | sfSequence
}

func waitNextMilli(lastTime int64) int64 {
	now := time.Now().UnixMilli()
	for now <= lastTime {
		time.Sleep(100 * time.Microsecond)
		now = time.Now().UnixMilli()
	}
	return now
}
