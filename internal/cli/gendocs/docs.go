package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/andrewheberle/am-autotask/internal/cli"
)

const (
	// docsDir is the directory the documentation is generated in, relative to
	// this package, which is the working directory for go generate and go test.
	docsDir = "../../../docs"

	// docsPrefix is the file name prefix of all generated documentation.
	docsPrefix = "am-autotask"
)

// docDefaults overrides flag defaults, by command path then flag name, that
// depend on the system the documentation is generated on.
var docDefaults = map[string]map[string]string{}

// generateDocs writes markdown documentation for each command to dir,
// replacing any previously generated documentation.
func generateDocs(dir string) error {
	root, err := cli.Command()
	if err != nil {
		return err
	}

	// remove previously generated docs so removed commands are not left behind
	existing, err := filepath.Glob(filepath.Join(dir, docsPrefix+"*.md"))
	if err != nil {
		return fmt.Errorf("finding existing docs: %w", err)
	}
	for _, f := range existing {
		if err := os.Remove(f); err != nil {
			return fmt.Errorf("removing existing docs: %w", err)
		}
	}

	return writeDocs(root, dir)
}

// writeDocs writes the documentation for cmd and each of its available
// subcommands to dir.
func writeDocs(cmd *cobra.Command, dir string) (err error) {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := writeDocs(c, dir); err != nil {
			return err
		}
	}

	for name, value := range docDefaults[cmd.CommandPath()] {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			return fmt.Errorf("overriding default of %q: flag %q not found", cmd.CommandPath(), name)
		}
		flag.DefValue = value
	}

	// avoid the generated date in the output so docs only change with the commands
	cmd.DisableAutoGenTag = true

	f, err := os.Create(filepath.Join(dir, docFilename(cmd.CommandPath())))
	if err != nil {
		return fmt.Errorf("creating docs for %q: %w", cmd.CommandPath(), err)
	}
	defer func() {
		err = errors.Join(err, f.Close())
	}()

	if err := doc.GenMarkdownCustom(cmd, f, docLink); err != nil {
		return fmt.Errorf("generating docs for %q: %w", cmd.CommandPath(), err)
	}

	return nil
}

// docFilename returns the file name for the docs of the command at path, such
// as "am-autotask-lookup-picklists.md" for "am-autotask lookup picklists".
func docFilename(path string) string {
	return strings.ReplaceAll(path, " ", "-") + ".md"
}

// docLink converts the link cobra generates, which joins command names with
// "_", to match the names from docFilename.
func docLink(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}
