package config

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ParseIntValue parses a complete integer value without truncation or overflow.
func ParseIntValue(path string, value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		if v >= int64(math.MinInt) && v <= int64(math.MaxInt) {
			return int(v), nil
		}
	case float64:
		// The positive bound is exclusive: float64(MaxInt) may round up to it.
		bound := math.Ldexp(1, strconv.IntSize-1)
		if !math.IsNaN(v) && !math.IsInf(v, 0) && math.Trunc(v) == v && v >= -bound && v < bound {
			return int(v), nil
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, nil
		}
	}
	return 0, configErrorf("%q must be an integer within the platform int range", path)
}

// ParseFloatValue parses a complete, finite numeric value.
func ParseFloatValue(path string, value any) (float64, error) {
	var n float64
	switch v := value.(type) {
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case float64:
		n = v
	case string:
		var err error
		n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, configErrorf("%q must be a finite number", path)
		}
	default:
		return 0, configErrorf("%q must be a finite number", path)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, configErrorf("%q must be a finite number", path)
	}
	return n, nil
}

func parseLLMNumericValue(path, field string, value any) (any, error) {
	if field == "max_tokens" {
		return ParseIntValue(path, value)
	}
	return ParseFloatValue(path, value)
}

func normalizeLLMNumericSource(data map[string]any, source string) (map[string]any, error) {
	fields := [...]string{"max_tokens", "temperature", "timeout_s"}
	out := maps.Clone(data)
	llm, _ := data["llm"].(map[string]any)
	llmCopy := maps.Clone(llm)
	for _, field := range fields {
		key := "llm_" + field
		flat, hasFlat := data[key]
		nested, hasNested := llm[field]
		if hasFlat && hasNested {
			return nil, configErrorf("%q specifies both %s and llm.%s; keep only one", source, key, field)
		}
		if !hasFlat && !hasNested {
			continue
		}
		value := flat
		if hasNested {
			value = nested
		}
		parsed, err := parseLLMNumericValue(source+": llm."+field, field, value)
		if err != nil {
			return nil, err
		}
		out[key] = parsed
		if hasNested {
			delete(llmCopy, field)
			out["llm"] = llmCopy
		}
	}
	if agents, ok := data["agents"].(map[string]any); ok {
		copiedAgents := maps.Clone(agents)
		for _, op := range slices.Sorted(maps.Keys(agents)) {
			overrides, ok := agents[op].(map[string]any)
			if !ok {
				continue
			}
			copied := maps.Clone(overrides)
			for _, field := range fields {
				value, exists := overrides[field]
				if !exists {
					continue
				}
				parsed, err := parseLLMNumericValue(source+": agents."+op+"."+field, field, value)
				if err != nil {
					return nil, err
				}
				copied[field] = parsed
			}
			copiedAgents[op] = copied
		}
		out["agents"] = copiedAgents
	}
	return out, nil
}
