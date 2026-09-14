package navorder

import (
	"encoding/json"
	"strings"
)

func NormalizeNavOrder(raw string, defaults []string) []string {
	var input []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &input); err != nil {
		return append([]string(nil), defaults...)
	}

	seen := make(map[string]struct{}, len(defaults))
	allowed := make(map[string]struct{}, len(defaults))
	for _, id := range defaults {
		allowed[id] = struct{}{}
	}

	out := make([]string, 0, len(defaults))
	for _, id := range input {
		id = strings.TrimSpace(id)
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, id := range defaults {
		if _, ok := seen[id]; ok {
			continue
		}
		out = append(out, id)
	}

	return out
}
