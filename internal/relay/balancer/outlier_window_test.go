package balancer

import (
	"testing"
	"time"
)

func resetOutlierConfig() { ConfigureOutlierWindow(defaultOutlierConfig) }

func reportOutlier(channelID int, success bool, n int, base time.Time) time.Time {
	t := base
	for i := 0; i < n; i++ {
		OutlierReport(channelID, success, 0, t)
		t = t.Add(time.Second)
	}
	return t
}

func TestOutlierGate1InsufficientSamples(t *testing.T) {
	resetOutlierConfig()
	const ch = 1001
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportOutlier(ch, false, 7, base)

	st := OutlierEvaluate(ch, base.Add(time.Minute))
	if st.Samples != 7 {
		t.Fatalf("Samples = %d, want 7", st.Samples)
	}
	if st.Candidate {
		t.Fatal("样本不足应 PASS（Candidate=false）")
	}
}

func TestOutlierGate1DilutedBySuccess(t *testing.T) {
	resetOutlierConfig()
	const ch = 1002
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := reportOutlier(ch, false, 10, base)
	reportOutlier(ch, true, 10, t2)

	st := OutlierEvaluate(ch, base.Add(time.Minute))
	if st.Samples != 20 {
		t.Fatalf("Samples = %d, want 20", st.Samples)
	}
	if st.Candidate {
		t.Fatal("有成功稀释应 PASS")
	}
}

func TestOutlierGate1ConsecutiveFailures(t *testing.T) {
	resetOutlierConfig()
	const ch = 1003
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportOutlier(ch, false, 12, base)

	st := OutlierEvaluate(ch, base.Add(time.Minute))
	if st.ConsecutiveFails != 12 {
		t.Fatalf("ConsecutiveFails = %d, want 12", st.ConsecutiveFails)
	}
	if !st.Candidate {
		t.Fatal("连续失败达标应成为候选")
	}
}

func TestOutlierGate1NoSuccessTriggersCandidate(t *testing.T) {
	resetOutlierConfig()
	const ch = 1004
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportOutlier(ch, false, 9, base)

	st := OutlierEvaluate(ch, base.Add(time.Minute))
	if !st.LastSuccessAt.IsZero() {
		t.Fatal("不应有成功记录")
	}
	if !st.Candidate {
		t.Fatal("窗口内无成功应成为候选")
	}
}

func TestOutlierGate1RecoveringNotCandidate(t *testing.T) {
	resetOutlierConfig()
	const ch = 1005
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := reportOutlier(ch, false, 18, base)
	reportOutlier(ch, true, 2, t2)

	st := OutlierEvaluate(ch, base.Add(time.Minute))
	if st.ConsecutiveFails != 0 {
		t.Fatalf("ConsecutiveFails = %d, want 0", st.ConsecutiveFails)
	}
	if st.Candidate {
		t.Fatal("正在恢复（最新成功）不应成为候选")
	}
}

func TestOutlierTimeExpiry(t *testing.T) {
	resetOutlierConfig()
	const ch = 1006
	ClearOutlierWindow(ch)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reportOutlier(ch, false, 12, base)

	st := OutlierEvaluate(ch, base.Add(20*time.Minute))
	if st.Samples != 0 {
		t.Fatalf("Samples = %d, want 0（应全部过期）", st.Samples)
	}
	if st.Candidate {
		t.Fatal("样本全过期应 PASS")
	}
}

func TestOutlierConfigureClamp(t *testing.T) {
	ConfigureOutlierWindow(OutlierConfig{Capacity: 999, TimeWindow: 0, MinSamples: 0, FailRate: 2, ConsecFails: 0})
	c := currentOutlierConfig()
	if c.Capacity != outlierPhysicalCap {
		t.Fatalf("Capacity = %d, 应封顶到 %d", c.Capacity, outlierPhysicalCap)
	}
	if c.FailRate != defaultOutlierConfig.FailRate {
		t.Fatal("非法 FailRate 应回退默认")
	}
	resetOutlierConfig()
}
