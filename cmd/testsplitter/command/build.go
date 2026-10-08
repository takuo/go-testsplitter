package command

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/takuo/go-testsplitter/internal/scanner"
)

// binaryName returns the test binary name for the package directory.
// e.g. api/service/foo → api.service.foo.test
func binaryName(dir string) string {
	return strings.ReplaceAll(dir, "/", ".") + ".test"
}

// goTestExecName returns the file name `go test -c -o DIR/` writes for the import path:
// the last path element, skipping a major version suffix such as "/v2".
func goTestExecName(importPath string) string {
	elem := path.Base(importPath)
	if elem != importPath && isMajorVersion(elem) {
		elem = path.Base(path.Dir(importPath))
	}
	return elem + ".test"
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	n, err := strconv.Atoi(s[1:])
	return err == nil && n >= 2 && strconv.Itoa(n) == s[1:]
}

// buildBatches groups packages so that the binary names written by `go test -c -o DIR/`
// do not collide within a batch. Usually all packages fit in a single batch.
func buildBatches(packages []scanner.Package) [][]scanner.Package {
	var batches [][]scanner.Package
	seen := make(map[string]int)
	for _, pkg := range packages {
		name := goTestExecName(pkg.ImportPath)
		i := seen[name]
		seen[name]++
		if i == len(batches) {
			batches = append(batches, nil)
		}
		batches[i] = append(batches[i], pkg)
	}
	return batches
}

// buildTestBinaries builds test binaries for all target packages into the binaries directory.
// Packages are built with a single `go test -c` invocation per batch, which loads the package graph
// only once, and the binaries are renamed to binaryName.
func (c *CLI) buildTestBinaries(ctx context.Context) error {
	if len(c.packages) == 0 {
		return nil
	}
	outputDir, err := filepath.Abs(c.BinariesDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	batches := buildBatches(c.packages)
	slog.Info("Building test binaries", "packages", len(c.packages), "batches", len(batches), "concurrency", c.BuildConcurrency)
	for _, batch := range batches {
		if err := c.buildBatch(ctx, outputDir, batch); err != nil {
			return err
		}
	}
	slog.Info("Built test binaries", "dir", outputDir)
	return nil
}

func (c *CLI) buildBatch(ctx context.Context, outputDir string, batch []scanner.Package) error {
	// Build into a temporary directory in outputDir so that renaming does not cross filesystems.
	tmpDir, err := os.MkdirTemp(outputDir, ".build-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	args := []string{"test", "-c", "-p", strconv.Itoa(c.BuildConcurrency), "-o", tmpDir + string(filepath.Separator)}
	for _, pkg := range batch {
		args = append(args, pkg.ImportPath)
		slog.Debug("Building test binary", "package", pkg.Dir, "binary", binaryName(pkg.Dir))
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go test -c: %w\n%s", err, out)
	}
	if len(out) > 0 {
		slog.Debug("go test -c output", "output", string(out))
	}

	for _, pkg := range batch {
		src := filepath.Join(tmpDir, goTestExecName(pkg.ImportPath))
		dst := filepath.Join(outputDir, binaryName(pkg.Dir))
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("failed to move test binary of %s: %w", pkg.Dir, err)
		}
	}
	return nil
}
