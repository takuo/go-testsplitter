package command

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/takuo/go-testsplitter/internal/gitdiff"
	"github.com/takuo/go-testsplitter/internal/scanner"
)

// selection is the result of selecting packages by changes.
type selection struct {
	since        string
	base         string
	changedFiles []string
	// runAllFile is the changed file which matched --run-all-on. Empty if not matched.
	runAllFile string
	// changedPackages is the import paths of changed packages.
	changedPackages []string
	// affectedBy maps a selected package directory to the changed packages its tests depend on.
	affectedBy map[string][]string
	total      int
}

// selectChangedPackages narrows down packages to those whose test binaries depend on packages
// changed since --changed-since, unless a changed file matches --run-all-on.
func (c *CLI) selectChangedPackages(ctx context.Context) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	changes, err := gitdiff.ChangedSince(ctx, cwd, c.ChangedSince)
	if err != nil {
		return err
	}
	sel := &selection{
		since:        c.ChangedSince,
		base:         changes.Base,
		changedFiles: changes.Files,
		affectedBy:   make(map[string][]string),
		total:        len(c.packages),
	}
	c.selection = sel

	if c.RunAllOn != "" {
		re := regexp.MustCompile(c.RunAllOn) // validated in Validate
		for _, f := range changes.Files {
			if re.MatchString(f) {
				sel.runAllFile = f
				slog.Info("Running all tests because a changed file matches --run-all-on", "file", f, "packages", len(c.packages))
				return nil
			}
		}
	}

	graph, err := scanner.LoadDepGraph(c.patterns)
	if err != nil {
		return err
	}
	files := make([]string, 0, len(changes.Files))
	for _, f := range changes.Files {
		files = append(files, filepath.Join(changes.Root, filepath.FromSlash(f)))
	}
	var unmatched []string
	sel.changedPackages, unmatched = graph.ChangedPackages(files)
	for _, f := range unmatched {
		slog.Debug("Changed file belongs to no package", "file", f)
	}

	var selected []scanner.Package
	for _, pkg := range c.packages {
		if via := graph.AffectedBy(pkg.ImportPath, sel.changedPackages); len(via) > 0 {
			selected = append(selected, pkg)
			sel.affectedBy[pkg.Dir] = via
			slog.Debug("Selected package", "package", pkg.Dir, "affected_by", via)
		}
	}
	c.packages = selected
	slog.Info("Selected packages affected by changes",
		"since", c.ChangedSince, "changed_files", len(changes.Files), "changed_packages", len(sel.changedPackages),
		"selected", len(selected), "total", sel.total)
	return nil
}
