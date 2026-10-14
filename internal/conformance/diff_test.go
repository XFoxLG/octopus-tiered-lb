package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
)

// diffEntry 描述一条规范化 JSON 差异。
// Path 使用 JSON Pointer 风格（根为 ""），Kind 取值：
// value_diff | type_diff | only_ours | only_oracle | length_diff。
type diffEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

func (d diffEntry) key() string { return d.Path + "|" + d.Kind }

func canonicalJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// 拒绝尾部多余内容，保证比较的是完整请求体。
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON 尾部存在多余内容")
	}
	return v, nil
}

func diffJSON(a, b any) []diffEntry {
	return diffAt("", a, b)
}

func diffAt(path string, a, b any) []diffEntry {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return []diffEntry{{Path: path, Kind: "type_diff"}}
		}
		keys := make([]string, 0, len(av)+len(bv))
		seen := make(map[string]bool, len(av)+len(bv))
		for k := range av {
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
			}
		}
		for k := range bv {
			if !seen[k] {
				keys = append(keys, k)
				seen[k] = true
			}
		}
		sort.Strings(keys)
		var out []diffEntry
		for _, k := range keys {
			child := path + "/" + escapeJSONPointer(k)
			avv, aok := av[k]
			bvv, bok := bv[k]
			switch {
			case aok && !bok:
				out = append(out, diffEntry{Path: child, Kind: "only_ours"})
			case !aok && bok:
				out = append(out, diffEntry{Path: child, Kind: "only_oracle"})
			default:
				out = append(out, diffAt(child, avv, bvv)...)
			}
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return []diffEntry{{Path: path, Kind: "type_diff"}}
		}
		if len(av) != len(bv) {
			return []diffEntry{{Path: path, Kind: "length_diff"}}
		}
		var out []diffEntry
		for i := range av {
			out = append(out, diffAt(fmt.Sprintf("%s/%d", path, i), av[i], bv[i])...)
		}
		return out
	default:
		if !scalarEqual(a, b) {
			return []diffEntry{{Path: path, Kind: "value_diff"}}
		}
		return nil
	}
}

func scalarEqual(a, b any) bool {
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok && bok {
		if an.String() == bn.String() {
			return true
		}
		af, _, err1 := big.ParseFloat(an.String(), 10, 128, big.ToNearestEven)
		bf, _, err2 := big.ParseFloat(bn.String(), 10, 128, big.ToNearestEven)
		if err1 == nil && err2 == nil {
			return af.Cmp(bf) == 0
		}
		return false
	}
	// 到达这里的只会是 string/bool/nil（map 与 slice 已在上面处理）。
	return a == b
}

func escapeJSONPointer(k string) string {
	k = strings.ReplaceAll(k, "~", "~0")
	return strings.ReplaceAll(k, "/", "~1")
}

func diffKeys(diffs []diffEntry) []string {
	keys := make([]string, 0, len(diffs))
	for _, d := range diffs {
		keys = append(keys, d.key())
	}
	sort.Strings(keys)
	return keys
}
