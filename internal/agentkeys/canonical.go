package agentkeys

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
)

func canonicalJSON(v any) ([]byte, error) {
	norm, err := normalize(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(norm)
}

func normalize(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case bool, float64, string:
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			n, err := normalize(t[k])
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		return normalize(decoded)
	}
}

func sha256Sum(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}
