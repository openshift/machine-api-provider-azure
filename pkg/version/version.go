package version

import (
	"fmt"
	"strings"

	"github.com/blang/semver"
)

var (
	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v0.0.0-was-not-built-properly"

	// Version is semver representation of the version.
	Version semver.Version

	// String is the human-friendly representation of the version.
	String string
)

func init() {
	// Try to parse the version string as semver
	v, err := semver.Parse(strings.TrimLeft(Raw, "v"))
	if err != nil {
		// If parsing fails (e.g., Raw is a commit hash like "2a0523c"),
		// fall back to a default version. This ensures the program can
		// still run even if the version wasn't properly set at build time
		// or if the repository has no git tags.
		Version = semver.MustParse("0.0.0")
	} else {
		Version = v
	}
	String = fmt.Sprintf("MachineAPIProviderAzure %s", Raw)
}
