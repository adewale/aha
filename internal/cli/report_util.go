package cli

func summarizeReports(reports []map[string]any) map[string]any {
	out := map[string]any{"sessions": 0, "entries": 0, "messages": 0, "images": 0, "artifacts": 0, "duplicate": false}
	for _, rep := range reports {
		for _, key := range []string{"sessions", "entries", "messages", "images", "artifacts"} {
			out[key] = out[key].(int) + intFromAny(rep[key])
		}
		if b, _ := rep["duplicate"].(bool); b {
			out["duplicate"] = true
		}
	}
	return out
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}
