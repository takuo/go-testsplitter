package command

import (
	"github.com/alecthomas/kong"
)

// Root is the testsplitter command line. Split is the default command, so that its flags and
// test flags can be given without naming it (`testsplitter -n 4 -- -test.timeout=10m`).
type Root struct {
	Split   CLI        `cmd:"" default:"withargs" help:"Split tests across nodes and generate test scripts (default)"`
	Archive ArchiveCmd `cmd:"" help:"Archive test results of a run as previous results for the next split"`

	Version kong.VersionFlag `short:"v" long:"version" help:"Print version and exit"`
}
