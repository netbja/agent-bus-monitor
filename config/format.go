package config

import (
	"strconv"
	"strings"
	"time"
)

// Format is a value as a person writes it: 5m, not 5m0s; 70, not 70.000000.
func Format(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Duration:
		if x == 0 {
			return "0"
		}
		s := x.String()
		if strings.HasSuffix(s, "m0s") {
			s = strings.TrimSuffix(s, "0s")
		}
		if strings.HasSuffix(s, "h0m") {
			s = strings.TrimSuffix(s, "0m")
		}
		return s
	case string:
		if x == "" {
			return `""`
		}
		return x
	}
	return "?"
}
