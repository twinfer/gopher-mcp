package outsidescope

import "time"

// This package is OUT of scope_packages, so even though time.Now is banned,
// the analyzer should not flag this call.
func Now() time.Time {
	return time.Now()
}
