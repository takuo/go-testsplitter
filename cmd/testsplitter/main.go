// Package main implements testsplitter, a tool for splitting Go tests across multiple nodes.
package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"

	"github.com/alecthomas/kong"

	"github.com/takuo/go-testsplitter/cmd/testsplitter/command"
)

func main() {
	cli := &command.CLI{}
	parser := kong.Must(cli, &kong.Vars{"version": fmt.Sprintf("testsplitter: %s", command.Version())},
		kong.Name("testsplitter"),
		kong.Description("Split Go tests across multiple nodes."),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)
	ctx, err := parser.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Failed to parse arguments: %v", err)
	}
	if err := ctx.Run(); err != nil {
		log.Fatal(err)
	}
}
