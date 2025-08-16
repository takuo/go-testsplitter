package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ScanPackages scans the specified directory for Go packages, excluding any that match the given pattern.
func ScanPackages(excludePattern string) ([]string, error) {
	var excludeRegex *regexp.Regexp
	var err error

	if excludePattern != "" {
		excludeRegex, err = regexp.Compile(excludePattern)
		if err != nil {
			return nil, fmt.Errorf("invalid exclude pattern: %v", err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("%w: could not get current directory", err)
	}
	cmd := exec.Command("go", "list", "-test", "-f", `{"Name":"{{ .Name }}","Dir":"{{.Dir}}","Root":"{{.Root}}","ImportPath":"{{.ImportPath}}"}`, "./...")
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to run go list: %v", err, string(output))
	}
	type Package struct {
		Dir        string
		ImportPath string
		Root       string
	}

	lines := strings.Split(string(output), "\n")
	packages := make([]string, 0, len(lines)/2)
	for _, line := range lines {
		if line == "" {
			continue
		}
		var pkg Package
		if err := json.Unmarshal([]byte(line), &pkg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal package info: %w", err)
		}
		if !strings.HasSuffix(pkg.ImportPath, ".test") {
			continue
		}
		if excludeRegex != nil && excludeRegex.MatchString(pkg.ImportPath) {
			continue
		}
		relPath, err := filepath.Rel(pkg.Root, pkg.Dir)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path for %s: %w", pkg.Dir, err)
		}
		packages = append(packages, relPath)
	}
	return packages, err
}
