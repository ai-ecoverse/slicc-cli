package update

import (
	"strconv"
	"strings"
)

func IsReleaseVersion(v string) bool {
	trimmed := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if trimmed == "" {
		return false
	}
	for _, part := range strings.Split(trimmed, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func IsNewer(latest, current string) bool {
	a, b := segments(latest), segments(current)
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

func Normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func segments(v string) []int {
	parts := strings.Split(Normalize(v), ".")
	nums := make([]int, len(parts))
	for i, part := range parts {
		if n, err := strconv.Atoi(part); err == nil {
			nums[i] = n
		}
	}
	return nums
}
