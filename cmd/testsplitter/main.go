// Package main implements testsplitter, a tool for splitting Go tests across multiple nodes.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/alecthomas/kong"

	"github.com/takuo/go-testsplitter/cmd/testsplitter/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cli := &command.CLI{}
	kctx := kong.Parse(cli,
		kong.Vars{"version": fmt.Sprintf("testsplitter: %s", command.Version())},
		kong.Name("testsplitter"),
		kong.Description("Split Go tests across multiple nodes."),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	kctx.FatalIfErrorf(kctx.Run())
}
