// Command gendocs generates the markdown documentation for am-autotask's
// commands into the docs directory of the repository.
//
// It is run by "go generate ./..." from the root of the repository, which runs
// it from this directory, and is not part of the am-autotask binary.
package main

//go:generate go run .

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if len(os.Args) != 1 {
		logger.Error("gendocs takes no arguments")
		os.Exit(2)
	}

	if err := generateDocs(docsDir); err != nil {
		logger.Error("could not generate docs", "error", err)
		os.Exit(1)
	}
}
