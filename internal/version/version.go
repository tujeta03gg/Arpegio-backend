// Package version identifies the running build. Release builds set Version with
// -ldflags "-X arpegio/internal/version.Version=1.2.3".
package version

import (
	"runtime"
	"runtime/debug"
)

// Version is the release number; "dev" for local builds.
var Version = "dev"

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get describes this build; the commit comes from the VCS data Go embeds at build time.
func Get() Info {
	info := Info{Version: Version, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				info.Commit = s.Value[:7]
			}
		}
	}
	return info
}
