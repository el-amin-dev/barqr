// Package version carries the build-time identity of the barqr binary.
//
// The variables in this package are overwritten at link time via
// -ldflags "-X github.com/el-amin-dev/barqr/internal/version.Version=..."
// so that a released binary can report exactly what it was built from.
package version

import (
	"fmt"
	"runtime"
)

// Build-time identity. Defaults describe a plain `go build` with no ldflags.
var (
	// Version is the semantic version of the release, e.g. "v1.2.3".
	Version = "dev"
	// Commit is the git revision the binary was built from.
	Commit = "none"
	// Date is the RFC 3339 build timestamp, or "unknown".
	Date = "unknown"
)

// Info is the machine-readable build identity, as served by /v1/version.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	Go        string `json:"go"`
	Platform  string `json:"platform"`
	Name      string `json:"name"`
	UserAgent string `json:"-"`
}

// Name is the product name reported by /v1/version and the User-Agent header.
const Name = "barqr"

// Get returns the build identity of the running binary.
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		Go:        runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Name:      Name,
		UserAgent: Name + "/" + Version,
	}
}

// String renders the build identity as a single human-readable line.
func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit %s, built %s, %s, %s)",
		i.Name, i.Version, i.Commit, i.Date, i.Go, i.Platform)
}
