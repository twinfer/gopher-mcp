package insidescope

import (
	"os"
	"time"
)

// Now exercises a banned package-qualified call. The analyzer is configured
// with `banned: [time.Now, os.Getenv]` and `scope_packages: [insidescope]`,
// so this package is in scope and both calls should be flagged.
func Now() time.Time {
	return time.Now() // want `call to banned time\.Now`
}

// Env is also flagged: os.Getenv is banned and we're in scope.
func Env() string {
	return os.Getenv("HOME") // want `call to banned os\.Getenv`
}

// fmt.Println is NOT banned — should not be flagged.
func OK() {
	_ = time.Time{}
}
