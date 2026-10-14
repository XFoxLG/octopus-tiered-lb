package conformance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestConformance 是本仓库协议层的三方对拍主入口：
//
//	我方引擎（inbound 解析 + 出站序列化） vs axonhub（主裁判） vs cliproxyapi（次裁判）
//
// 没有提供 oracle 二进制（未设置环境变量）时，只复现我方当前行为并写日志，
// 供 build-and-test 等常规 job 使用；提供 oracle 后（CI 的 conformance-oracle
// job），未登记差异会让测试变红。
func TestConformance(t *testing.T) {
	root := repoRoot(t)
	samples := loadSamples(t, filepath.Join(root, "conformance", "samples"))
	if len(samples) == 0 {
		t.Fatal("conformance/samples 下没有任何样本")
	}
	reg, err := loadRegistry(filepath.Join(root, "conformance", "deviations.json"), samples)
	if err != nil {
		t.Fatalf("加载登记簿失败: %v", err)
	}

	report := &conformanceReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	defer writeReport(t, report)

	ctx := context.Background()
	for _, s := range samples {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			if s.Side != "request" {
				report.Samples = append(report.Samples, reportSample{
					ID: s.ID, Class: s.Class, Inbound: s.Inbound, Outbound: s.Outbound,
					Side: s.Side, Note: "第二阶段样本，本阶段跳过",
				})
				t.Skipf("side=%s（第二阶段执行）：%s", s.Side, s.Class)
				return
			}

			ours := runOurs(ctx, s)
			rs := reportSample{
				ID: s.ID, Class: s.Class, Inbound: s.Inbound, Outbound: s.Outbound,
				Ours: &ours, Oracles: map[string]reportOracle{},
			}
			if ours.Kind == "ok" {
				t.Logf("ours: ok %s %s (%d bytes)", ours.Method, ours.Path, len(ours.Body))
			} else {
				t.Logf("ours: %s: %s", ours.Kind, truncateText(ours.Error, 300))
			}

			for _, spec := range oracleSpecs {
				bin := os.Getenv(spec.Env)
				if bin == "" {
					rs.Oracles[spec.Name] = reportOracle{
						Outcome:  outcome{Kind: "not_provided"},
						Registry: "not_provided",
					}
					t.Logf("%s: 未提供二进制（%s 未设置），跳过对拍", spec.Name, spec.Env)
					continue
				}
				orc := runOracle(ctx, bin, s)
				diffs, endpointDiff := compareOutcomes(ours, orc)
				status, checkErr := reg.check(s.ID, spec.Name, ours, orc, diffs, endpointDiff)
				rs.Oracles[spec.Name] = reportOracle{
					Outcome: orc, Diff: diffs, EndpointDiff: endpointDiff, Registry: status,
				}
				t.Logf("%s: %s (registry=%s, diff=%d, endpoint_diff=%v)",
					spec.Name, orc.Kind, status, len(diffs), endpointDiff)
				if checkErr != nil {
					t.Errorf("%v", checkErr)
				}
				if status == "pending" {
					t.Logf("警告：%s vs %s 仍处于 pending 校准状态，请在后续 PR 完成登记", s.ID, spec.Name)
				}
				if status == "stale" {
					t.Logf("提示：%s vs %s 的登记条目已过期（当前无差异），可删除该条目", s.ID, spec.Name)
				}
			}
			report.Samples = append(report.Samples, rs)
		})
	}
}

// compareOutcomes 计算双方都成功时的差异；任一方失败时返回空差异，
// 由登记簿以 outcome 形式对失败情形分类。
func compareOutcomes(ours, orc outcome) ([]diffEntry, bool) {
	if ours.Kind != "ok" || orc.Kind != "ok" {
		return nil, false
	}
	a, errA := canonicalJSON(ours.Body)
	b, errB := canonicalJSON(orc.Body)
	if errA != nil || errB != nil {
		return []diffEntry{{Path: "", Kind: "type_diff"}}, false
	}
	diffs := diffJSON(a, b)
	endpointDiff := endpointString(ours) != "" && endpointString(orc) != "" && endpointString(ours) != endpointString(orc)
	return diffs, endpointDiff
}

func endpointString(o outcome) string {
	if o.Method == "" && o.Path == "" {
		return ""
	}
	return o.Method + " " + o.Path
}
