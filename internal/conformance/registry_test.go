package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// deviationsFile 是 conformance/deviations.json 的模型：登记簿记录每一条
// 「已接受的三方差异」，CI 遇到未登记差异即红。
type deviationsFile struct {
	Version int              `json:"version"`
	Notes   string           `json:"notes,omitempty"`
	Entries []deviationEntry `json:"entries"`
}

type deviationEntry struct {
	Sample string `json:"sample"`
	Oracle string `json:"oracle"`
	// Outcome 取值：
	//   diff                   双方都成功、存在差异（差异项登记在 DiffPaths）
	//   pending                首次校准尚未完成：容忍一切差异（CI 会告警）
	//   unsupported            oracle 没有该方向的转换器（非差异，显式登记）
	//   ours_error_oracle_ok   我方转换失败、oracle 成功
	//   ours_ok_oracle_error   我方成功、oracle 失败
	//   both_error             双方都失败（错误文案不比较）
	Outcome      string   `json:"outcome"`
	DiffPaths    []string `json:"diff_paths,omitempty"` // 形如 "/max_tokens|value_diff"
	EndpointDiff bool     `json:"endpoint_diff,omitempty"`
	Note         string   `json:"note,omitempty"`
}

var validRegistryOutcomes = map[string]bool{
	"diff":                 true,
	"pending":              true,
	"unsupported":          true,
	"ours_error_oracle_ok": true,
	"ours_ok_oracle_error": true,
	"both_error":           true,
}

type registry struct {
	file    deviationsFile
	byKey   map[string]deviationEntry
	samples map[string]bool
}

func loadRegistry(path string, samples []sample) (*registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取登记簿 %s: %w", path, err)
	}
	var f deviationsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("解析登记簿 %s: %w", path, err)
	}
	reg := &registry{file: f, byKey: map[string]deviationEntry{}, samples: map[string]bool{}}
	for _, s := range samples {
		reg.samples[s.ID] = true
	}
	for i, e := range f.Entries {
		if e.Sample == "" || e.Oracle == "" {
			return nil, fmt.Errorf("登记簿第 %d 条缺少 sample/oracle", i+1)
		}
		if !reg.samples[e.Sample] {
			return nil, fmt.Errorf("登记簿第 %d 条引用了不存在的样本 %q", i+1, e.Sample)
		}
		if e.Oracle != "axonhub" && e.Oracle != "cliproxyapi" {
			return nil, fmt.Errorf("登记簿第 %d 条 oracle %q 未知", i+1, e.Oracle)
		}
		if !validRegistryOutcomes[e.Outcome] {
			return nil, fmt.Errorf("登记簿第 %d 条 outcome %q 未知", i+1, e.Outcome)
		}
		key := e.Sample + "|" + e.Oracle
		if _, dup := reg.byKey[key]; dup {
			return nil, fmt.Errorf("登记簿存在重复条目 %s", key)
		}
		reg.byKey[key] = e
	}
	return reg, nil
}

func (r *registry) find(sampleID, oracle string) (deviationEntry, bool) {
	e, ok := r.byKey[sampleID+"|"+oracle]
	return e, ok
}

// check 对照一条结果与登记簿，返回登记状态与可执行的错误说明。
func (r *registry) check(sampleID, oracle string, ours, orc outcome, diffs []diffEntry, endpointDiff bool) (string, error) {
	entry, has := r.find(sampleID, oracle)

	if orc.Kind == "unsupported" {
		if !has || entry.Outcome != "unsupported" {
			return "missing", fmt.Errorf(
				"%s vs %s: oracle 报告该方向无转换器；请在 conformance/deviations.json 增加 outcome=unsupported 条目并写明理由",
				sampleID, oracle)
		}
		return "registered", nil
	}
	if has && entry.Outcome == "pending" {
		return "pending", nil
	}

	oursFailed := ours.Kind != "ok"
	oracleFailed := orc.Kind != "ok"

	switch {
	case oursFailed && oracleFailed:
		if !has || entry.Outcome != "both_error" {
			return "missing", fmt.Errorf(
				"%s vs %s: 双方都失败（ours=%q oracle=%q）；如确认可接受请登记 outcome=both_error",
				sampleID, oracle, truncateText(ours.Error, 160), truncateText(orc.Error, 160))
		}
		return "registered", nil
	case oursFailed && !oracleFailed:
		if !has || entry.Outcome != "ours_error_oracle_ok" {
			return "missing", fmt.Errorf(
				"%s vs %s: 我方失败而 oracle 成功（ours=%q）；登记 outcome=ours_error_oracle_ok 或在修复后重新校准",
				sampleID, oracle, truncateText(ours.Error, 200))
		}
		return "registered", nil
	case !oursFailed && oracleFailed:
		if !has || entry.Outcome != "ours_ok_oracle_error" {
			return "missing", fmt.Errorf(
				"%s vs %s: oracle 失败而我方成功（oracle=%q）；登记 outcome=ours_ok_oracle_error 或在确认 oracle 缺陷后登记",
				sampleID, oracle, truncateText(orc.Error, 200))
		}
		return "registered", nil
	}

	// 双方都成功：比较请求体与端点。
	if len(diffs) == 0 && !endpointDiff {
		if has && entry.Outcome == "diff" {
			return "stale", nil
		}
		return "clean", nil
	}
	if !has || entry.Outcome != "diff" {
		return "missing", fmt.Errorf(
			"%s vs %s: 存在未登记差异（%d 项，endpoint_diff=%v）：%s\n请从 CI artifact conformance-report.json 校准 conformance/deviations.json",
			sampleID, oracle, len(diffs), endpointDiff, summarizeDiffKeys(diffKeys(diffs), endpointDiff))
	}
	observed := append([]string{}, diffKeys(diffs)...)
	if endpointDiff {
		observed = append(observed, "endpoint|diff")
	}
	registered := map[string]bool{}
	for _, p := range entry.DiffPaths {
		registered[p] = true
	}
	if endpointDiff && entry.EndpointDiff {
		registered["endpoint|diff"] = true
	}
	var missing []string
	for _, o := range observed {
		if !registered[o] {
			missing = append(missing, o)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "mismatch", fmt.Errorf(
			"%s vs %s: 出现登记簿未登记的新差异项：%s\n请重新校准 conformance/deviations.json（或修复转换）",
			sampleID, oracle, strings.Join(missing, ", "))
	}
	return "registered", nil
}

func summarizeDiffKeys(keys []string, endpointDiff bool) string {
	const maxShow = 12
	shown := keys
	truncated := false
	if len(shown) > maxShow {
		shown = shown[:maxShow]
		truncated = true
	}
	parts := append([]string{}, shown...)
	if endpointDiff {
		parts = append(parts, "endpoint|diff")
	}
	out := strings.Join(parts, ", ")
	if truncated {
		out += fmt.Sprintf(" …(共 %d 项)", len(keys))
	}
	return out
}
