package config

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ParseSize reads a memory size in systemd's syntax (agent-resource-limits
// §8): a number of bytes above 0, or one with a K, M, G or T suffix, base 1024
// ("8G", "1.5G", "512M"). Nothing else: no "B", no space, no percentage.
func ParseSize(s string) (int64, error) {
	bad := errors.New(`want a size such as "8G" or "512M"`)
	num, mult := s, 1.0
	if n := len(s); n > 0 {
		if i := strings.IndexByte("KMGT", s[n-1]); i >= 0 {
			num, mult = s[:n-1], math.Pow(1024, float64(i+1))
		}
	}
	if num == "" || strings.Trim(num, "0123456789.") != "" || num[0] == '.' || num[len(num)-1] == '.' {
		return 0, bad
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f <= 0 || f*mult >= math.MaxInt64 || int64(f*mult) < 1 {
		return 0, bad
	}
	return int64(f * mult), nil
}

// fileSizeOrOff is a size (ParseSize) or "off".
func fileSizeOrOff(v any) (any, string) {
	const want = `want a size such as "8G", or "off"`
	s, ok := v.(string)
	if !ok {
		return nil, want
	}
	if s == "off" {
		return s, ""
	}
	if _, err := ParseSize(s); err != nil {
		return nil, want
	}
	return s, ""
}
