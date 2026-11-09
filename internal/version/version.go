// Package version holds build metadata injected via -ldflags (docs/13 §1).
package version

// Version follows SemVer; the default is the P0 baseline and is overridden at build time.
var (
	Version = "0.1.0"
	BuildID = "dev"
	Commit  = "unknown"
)

// String returns the human-readable build identity used by /healthz and --version.
func String() string { return Version + "+" + BuildID }
