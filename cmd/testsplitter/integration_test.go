package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

// buildBinary builds testsplitter into a temporary directory.
func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "testsplitter")
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	require.NoError(t, err, "Failed to build testsplitter: %s", out)
	return binary
}

func testdataDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata")
	require.NoError(t, err)
	return dir
}

func runSplitter(t *testing.T, binary, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "testsplitter should run successfully. Output: %s", output)
	return string(output)
}

// assertGolden compares content with the golden file, after replacing the environment dependent directory.
func assertGolden(t *testing.T, content, baseDir, goldenFile string) {
	t.Helper()
	content = strings.ReplaceAll(content, baseDir, "@TESTDATA@")
	if *update {
		require.NoError(t, os.WriteFile(goldenFile, []byte(content), 0o644))
	}
	want, err := os.ReadFile(goldenFile)
	require.NoError(t, err, "run 'go test . -update' to create golden files")
	assert.Equal(t, string(want), content, "run 'go test . -update' to update golden files")
}

func TestMainIntegration(t *testing.T) {
	const nodes = 2
	binary := buildBinary(t)
	dir := testdataDir(t)
	goldenDir := filepath.Join(dir, "golden")

	// Relative directories and import paths produce the same scripts
	inputs := map[string]string{
		"directories": "example/pkg1\nexample/pkg2\nexample/pkg3\n",
		"import paths": "github.com/takuo/go-testsplitter/cmd/testsplitter/testdata/example/pkg1\n" +
			"github.com/takuo/go-testsplitter/cmd/testsplitter/testdata/example/pkg2\n" +
			"github.com/takuo/go-testsplitter/cmd/testsplitter/testdata/example/pkg3\n",
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			outputDir := t.TempDir()
			output := runSplitter(t, binary, dir, input,
				"-d", "-n", strconv.Itoa(nodes), "-o", outputDir, "--", "-test.timeout=20m", "-test.v")
			assert.Contains(t, output, "Loaded 9 testcases durations from 3 files in ./test-json")

			for i := range nodes {
				name := "test-node-" + strconv.Itoa(i) + ".sh"
				b, err := os.ReadFile(filepath.Join(outputDir, name))
				require.NoError(t, err, "Output file should be created")
				assertGolden(t, string(b), dir, filepath.Join(goldenDir, name+".golden"))
			}
		})
	}
}

func TestInvalidArguments(t *testing.T) {
	binary := buildBinary(t)
	out, err := exec.Command(binary, "-n", "0", "-d").CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "--nodes must be >= 1")
}
