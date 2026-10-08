package scanner

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// setupModule creates a module with packages:
//
//	pkg1 (test), pkg2 (test), pkg2/subpkg (external test only), notest (no test), tagged (test only with build tag)
func setupModule(t *testing.T) string {
	t.Helper()
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "go.mod"), "module example.com/testpkg\n\ngo 1.21\n")
	writeFile(t, filepath.Join(baseDir, "pkg1", "pkg1.go"), "package pkg1\n")
	writeFile(t, filepath.Join(baseDir, "pkg1", "pkg1_test.go"), "package pkg1\n")
	writeFile(t, filepath.Join(baseDir, "pkg2", "pkg2.go"), "package pkg2\n")
	writeFile(t, filepath.Join(baseDir, "pkg2", "pkg2_test.go"), "package pkg2\n")
	writeFile(t, filepath.Join(baseDir, "pkg2", "subpkg", "subpkg.go"), "package subpkg\n")
	writeFile(t, filepath.Join(baseDir, "pkg2", "subpkg", "subpkg_test.go"), "package subpkg_test\n")
	writeFile(t, filepath.Join(baseDir, "notest", "notest.go"), "package notest\n")
	writeFile(t, filepath.Join(baseDir, "tagged", "tagged.go"), "package tagged\n")
	writeFile(t, filepath.Join(baseDir, "tagged", "tagged_test.go"), "//go:build integration\n\npackage tagged\n")
	return baseDir
}

func dirs(pkgs []Package) []string {
	var ds []string
	for _, p := range pkgs {
		ds = append(ds, p.Dir)
	}
	return ds
}

func TestListPackages(t *testing.T) {
	baseDir := setupModule(t)
	t.Chdir(baseDir)

	t.Run("scan all", func(t *testing.T) {
		got, err := ListPackages([]string{"./..."}, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"pkg1", "pkg2", "pkg2/subpkg"}, dirs(got))
		assert.Equal(t, "example.com/testpkg/pkg1", got[0].ImportPath)
		assert.Equal(t, []string{filepath.Join(baseDir, "pkg2", "subpkg", "subpkg_test.go")}, got[2].TestFiles)
	})

	t.Run("exclude", func(t *testing.T) {
		got, err := ListPackages([]string{"./..."}, regexp.MustCompile("subpkg"))
		require.NoError(t, err)
		assert.Equal(t, []string{"pkg1", "pkg2"}, dirs(got))
	})

	t.Run("import paths", func(t *testing.T) {
		got, err := ListPackages([]string{"example.com/testpkg/pkg2", "example.com/testpkg/pkg1"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"pkg1", "pkg2"}, dirs(got))
	})

	t.Run("bare relative directories", func(t *testing.T) {
		got, err := ListPackages([]string{"pkg2/subpkg", "./pkg1", "pkg1"}, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"pkg1", "pkg2/subpkg"}, dirs(got))
	})

	t.Run("unknown package", func(t *testing.T) {
		_, err := ListPackages([]string{"example.com/testpkg/nonexistent"}, nil)
		assert.Error(t, err)
	})
}

func TestListPackages_FromSubdirectory(t *testing.T) {
	baseDir := setupModule(t)
	t.Chdir(filepath.Join(baseDir, "pkg2"))

	got, err := ListPackages([]string{"./..."}, nil)
	require.NoError(t, err)
	// Relative to the current directory, not the module root
	assert.Equal(t, []string{".", "subpkg"}, dirs(got))
}
