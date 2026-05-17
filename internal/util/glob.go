package util

import (
	"regexp"
	"strings"
)

// MatchPackagePath reports whether path matches a Go-style package pattern.
// Supported: exact match; trailing /... matches the named package and all
// sub-packages; empty pattern or "..." matches anything.
func MatchPackagePath(pattern, path string) bool {
	if pattern == "" || pattern == "..." {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/..."); ok {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return pattern == path
}

// CompileNameGlob compiles a name pattern where '*' matches any run of
// characters and everything else is literal. Returns nil for empty input.
func CompileNameGlob(pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
}
