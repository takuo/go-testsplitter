package command

import (
	"runtime/debug"
)

// go build -ldflags="-X github.com/takuo/go-testsplitter/cmd/testsplitter/command.version=`git log --pretty=format:%h -n 1`"
var version string

// Version returns version string
func Version() string {
	if version != "" {
		return version
	}

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		return buildInfo.Main.Version
	}
	return "unknown"
}
