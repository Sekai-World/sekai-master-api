// Package version holds build-time metadata injected into release binaries
// via `-ldflags "-X sekai-master-api/internal/version.Version=..."`.
package version

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// IsRelease reports whether the binary was built with a real release version
// instead of the development default.
func IsRelease() bool {
	return Version != "dev"
}
