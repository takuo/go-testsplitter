package scanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanPackages(t *testing.T) {
	baseDir := t.TempDir()

	// サブディレクトリ作成
	pkg1 := filepath.Join(baseDir, "pkg1")
	pkg2 := filepath.Join(baseDir, "pkg2")
	subpkg := filepath.Join(pkg2, "subpkg")
	require.NoError(t, os.MkdirAll(pkg1, 0o755))
	require.NoError(t, os.MkdirAll(subpkg, 0o755))

	// Goファイル作成
	require.NoError(t, os.WriteFile(filepath.Join(pkg1, "pkg1.go"), []byte("package pkg1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg1, "pkg1_test.go"), []byte("package pkg1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg2, "pkg2.go"), []byte("package pkg2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkg2, "pkg2_test.go"), []byte("package pkg2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subpkg, "subpkg.go"), []byte("package subpkg\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subpkg, "subpkg_test.go"), []byte("package subpkg\n"), 0o644))

	t.Chdir(baseDir)
	os.WriteFile(filepath.Join(baseDir, "main.go"), []byte(`package main
import (
_ "example.com/testpkg/pkg1"
_ "example.com/testpkg/pkg2"
_ "example.com/testpkg/pkg2/subpkg"
)
func main() {}
`), 0o644)

	cmd := exec.Command("go", "mod", "init", "example.com/testpkg")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to initialize go module: %s", output)
	cmd = exec.Command("go", "mod", "tidy")
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, "Failed to tidy go module: %s", output)

	// 除外パターンなし
	got, err := ScanPackages("")
	require.NoError(t, err)
	want := []string{
		filepath.Join("pkg1"),
		filepath.Join("pkg2"),
		filepath.Join("pkg2", "subpkg"),
	}
	slices.Sort(got)
	slices.Sort(want)
	assert.Equal(t, want, got)

	// 除外パターンあり
	got, err = ScanPackages("subpkg")
	require.NoError(t, err)
	want = []string{
		filepath.Join("pkg1"),
		filepath.Join("pkg2"),
	}
	slices.Sort(got)
	slices.Sort(want)
	assert.Equal(t, want, got)
}
