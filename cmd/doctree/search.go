package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/hexops/cmder"
	"github.com/pkg/errors"

	// Register language indexers.
	"github.com/sourcegraph/doctree/doctree/indexer"
	_ "github.com/sourcegraph/doctree/doctree/indexer/golang"
	_ "github.com/sourcegraph/doctree/doctree/indexer/markdown"
	_ "github.com/sourcegraph/doctree/doctree/indexer/python"
)

func init() {
	const usage = `
Examples:

  Search :

    $ doctree search 'myquery'

`

	// Parse flags for our subcommand.
	flagSet := flag.NewFlagSet("search", flag.ExitOnError)
	dataDirFlag := flagSet.String("data-dir", defaultDataDir(), "where doctree stores its data")
	projectNameFlag := flagSet.String("project", "", "search in a specific project")

	// Handles calls to our subcommand.
	handler := func(args []string) error {
		_ = flagSet.Parse(args)
		if flagSet.NArg() != 1 {
			return &cmder.UsageError{}
		}
		query := flagSet.Arg(0)

		ctx := context.Background()
		indexDataDir := filepath.Join(*dataDirFlag, "index")
		_, err := indexer.Search(ctx, indexDataDir, query, *projectNameFlag)
		if err != nil {
			return errors.Wrap(err, "Search")
		}

		// TODO: CLI interface for search! Print the results here at least :)
		return nil
	}

	// Register the command.
	commands = append(commands, &cmder.Command{
		FlagSet: flagSet,
		Aliases: []string{},
		Handler: handler,
		UsageFunc: func() {
			fmt.Fprintf(flag.CommandLine.Output(), "Usage of 'doctree %s':\n", flagSet.Name())
			flagSet.PrintDefaults()
			fmt.Fprintf(flag.CommandLine.Output(), "%s", usage)
		},
	})
}
