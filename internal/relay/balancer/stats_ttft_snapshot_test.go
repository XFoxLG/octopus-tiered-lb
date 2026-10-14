package balancer

import (
	"fmt"
	"testing"
	"time"
)

// TestGetAutoStatsSnapshot_CarriesTTFT 验证 Auto 快照把 TTFT EMA 与首字样本数
// 一起透出。分析中心依赖这两个字段渲染「可见首字」并识别疑似伪流式渠道
// （首字占满单次时长），所以它们必须出现在 GetAutoStatsSnapshot 的结果里。
func TestGetAutoStatsSnapshot_CarriesTTFT(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("ttft-snapshot-%d", time.Now().UnixNano())
	RecordAutoTTFT(37, modelName, 100)
	RecordAutoTTFT(37, modelName, 200)

	snapshot := GetAutoStatsSnapshot([]int{37})
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}
	item := snapshot[0]
	// alpha=0.3：100 -> 0.3*200 + 0.7*100 = 130
	if item.AvgTTFTMs < 129.99 || item.AvgTTFTMs > 130.01 {
		t.Fatalf("AvgTTFTMs = %f, want ~130", item.AvgTTFTMs)
	}
	if item.TTFTSamples != 2 {
		t.Fatalf("TTFTSamples = %d, want 2", item.TTFTSamples)
	}
}

// TestGetAutoStatsSnapshot_NoTTFTEvidence 验证只有总延迟样本（非流式流量）时
// 首字字段保持 0：前端据此区分「首字为 0」与「没有流式首字样本」，不误标。
func TestGetAutoStatsSnapshot_NoTTFTEvidence(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("ttft-empty-%d", time.Now().UnixNano())
	RecordAutoLatency(38, modelName, 500)

	snapshot := GetAutoStatsSnapshot([]int{38})
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}
	item := snapshot[0]
	if item.AvgTTFTMs != 0 || item.TTFTSamples != 0 {
		t.Fatalf("AvgTTFTMs=%f TTFTSamples=%d, want 0/0", item.AvgTTFTMs, item.TTFTSamples)
	}
	if item.AvgLatencyMs != 500 {
		t.Fatalf("AvgLatencyMs = %f, want 500", item.AvgLatencyMs)
	}
}
