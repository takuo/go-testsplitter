package command

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/takuo/go-testsplitter/internal/gitdiff"
	"github.com/takuo/go-testsplitter/internal/impact"
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

	// The following are set with --granularity symbol.
	symbol bool
	// symbolError is why the symbol analysis failed and packages are selected by package.
	symbolError    string
	changedSymbols []string
	// tests maps a package directory to the selected test functions and a changed declaration each depends on.
	// Packages whose all tests are selected are in allReasons instead.
	tests      map[string]map[string]string
	allReasons map[string]string
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

	if c.Granularity == "symbol" && len(c.packages) > 0 {
		c.selectBySymbols(ctx, changes, graph)
	}
	return nil
}

// selectBySymbols narrows down tests of the selected packages to those referencing changed top-level
// declarations transitively. If the analysis fails, the packages selected by package are kept.
func (c *CLI) selectBySymbols(ctx context.Context, changes *gitdiff.Changes, graph *scanner.DepGraph) {
	sel := c.selection
	start := time.Now()
	candidates := make([]string, 0, len(c.packages))
	for _, pkg := range c.packages {
		candidates = append(candidates, pkg.ImportPath)
	}
	res, err := impact.Analyze(ctx, impact.Options{
		Root: changes.Root, Base: changes.Base, Files: changes.Files, Graph: graph, Candidates: candidates,
	})
	if err != nil {
		slog.Warn("Failed to analyze changed declarations; selecting all tests of affected packages", "err", err)
		sel.symbolError = err.Error()
		return
	}

	sel.symbol = true
	sel.changedSymbols = res.ChangedSymbols
	sel.tests = make(map[string]map[string]string)
	sel.allReasons = make(map[string]string)
	for pkg, reason := range res.Fallbacks {
		slog.Debug("Selecting all tests linking the package", "package", pkg, "reason", reason)
	}

	var selected []scanner.Package
	for _, pkg := range c.packages {
		s := res.Selections[pkg.ImportPath]
		if s == nil {
			continue
		}
		selected = append(selected, pkg)
		if s.All {
			sel.allReasons[pkg.Dir] = s.Reason
		} else {
			sel.tests[pkg.Dir] = s.Functions
		}
	}
	c.packages = selected
	slog.Info("Selected tests by changed declarations",
		"changed_symbols", len(res.ChangedSymbols), "packages", len(selected), "elapsed", time.Since(start).Round(time.Millisecond))
}

// filterTestFunctions keeps only the test functions selected by --granularity symbol.
func (c *CLI) filterTestFunctions() {
	if c.selection == nil || c.selection.tests == nil {
		return
	}
	for dir, funcs := range c.testFunctions {
		allowed, ok := c.selection.tests[dir]
		if !ok {
			continue // all tests
		}
		funcs = slices.DeleteFunc(funcs, func(fn string) bool {
			_, ok := allowed[fn]
			return !ok
		})
		if len(funcs) == 0 {
			delete(c.testFunctions, dir)
		} else {
			c.testFunctions[dir] = funcs
		}
	}
}
