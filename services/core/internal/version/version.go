// Package version carries the build version for both binaries.
//
// It exists so `matrix --version` and `matrixd -version` report something
// truthful. The installation docs told users to run `matrix --version` while
// the CLI answered "unknown flag: --version", so the command is now real rather
// than the instruction being deleted.
package version

// Version is the released version, injected at build time by the release
// workflow with:
//
//	-ldflags "-X github.com/ecirlabs/matrix-core/internal/version.Version=$TAG"
//
// A plain `go build` leaves it as "dev", which is the honest answer for a
// binary built from a working tree rather than from a tag.
var Version = "dev"

// String returns the version to report to users.
func String() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
