package main

import (
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/takuo/go-testsplitter/internal/parser"
	"github.com/takuo/go-testsplitter/internal/types"
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
			assert.Contains(t, output, `msg="Loaded test durations" tests=9 files=3 dir=./test-json`)

			for i := range nodes {
				name := "test-node-" + strconv.Itoa(i) + ".sh"
				b, err := os.ReadFile(filepath.Join(outputDir, name))
				require.NoError(t, err, "Output file should be created")
				assertGolden(t, string(b), dir, filepath.Join(goldenDir, name+".golden"))
			}
		})
	}
}

func TestScanPackagesWithExclude(t *testing.T) {
	binary := buildBinary(t)
	dir := filepath.Join(testdataDir(t), "example")
	outputDir := t.TempDir()

	runSplitter(t, binary, dir, "", "-d", "-s", "-x", "pkg2$", "-n", "1", "-o", outputDir, "-j", t.TempDir())

	b, err := os.ReadFile(filepath.Join(outputDir, "test-node-0.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "'pkg1'")
	assert.Contains(t, string(b), "'pkg3'")
	assert.NotContains(t, string(b), "'pkg2'")
}

func TestDryRunAndPlan(t *testing.T) {
	binary := buildBinary(t)
	dir := testdataDir(t)
	work := t.TempDir()
	scripts := filepath.Join(work, "scripts")
	planFile := filepath.Join(work, "plan.json")
	input := "example/pkg1\nexample/pkg2\nexample/pkg3\n"

	cmd := exec.Command(binary, "--dry-run", "-q", "-n", "2", "-o", scripts, "-p", filepath.Join(work, "bin"), "--plan", planFile)
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(), stderr.String())

	assert.Empty(t, stderr.String(), "quiet mode prints no info logs")
	assert.Contains(t, stdout.String(), "NODE  TESTS  PROCESSES  ESTIMATED")
	assert.Contains(t, stdout.String(), "Total 12 tests")
	assert.NoDirExists(t, scripts, "dry-run must not write scripts")
	assert.NoDirExists(t, filepath.Join(work, "bin"), "dry-run must not build binaries")

	b, err := os.ReadFile(planFile)
	require.NoError(t, err)
	var plan struct {
		TotalTests int `json:"total_tests"`
		Nodes      []struct {
			Index int `json:"index"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(b, &plan))
	assert.Equal(t, 12, plan.TotalTests)
	assert.Len(t, plan.Nodes, 2)

	// --plan - with --dry-run prints only JSON to stdout
	cmd = exec.Command(binary, "--dry-run", "-n", "2", "--plan", "-")
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.True(t, json.Valid(out), "%s", out)
}

func TestInvalidArguments(t *testing.T) {
	binary := buildBinary(t)
	out, err := exec.Command(binary, "-n", "0", "-d").CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "--nodes must be >= 1")
}

// TestEndToEnd builds test binaries, runs all generated scripts, and checks that every test ran exactly once.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if _, err := exec.LookPath("gotestsum"); err != nil {
		t.Skip("gotestsum is not installed")
	}
	const nodes = 3
	binary := buildBinary(t)

	// Copy example packages into an isolated module so that outputs don't pollute testdata
	work := t.TempDir()
	src := filepath.Join(testdataDir(t), "example")
	require.NoError(t, os.CopyFS(work, os.DirFS(src)))
	require.NoError(t, os.WriteFile(filepath.Join(work, "go.mod"), []byte("module example\n\ngo 1.21\n"), 0o644))

	runSplitter(t, binary, work, "", "-s", "-n", strconv.Itoa(nodes), "-c", "2", "-m", "1", "--", "-test.count=1")

	// The scripts must not need the Go toolchain: put a failing "go" first in PATH
	assert.FileExists(t, filepath.Join(work, "test-bin", "test2json"))
	noGo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(noGo, "go"), []byte("#!/bin/sh\necho go is not available >&2\nexit 1\n"), 0o755))

	for i := range nodes {
		script := filepath.Join(work, "test-scripts", "test-node-"+strconv.Itoa(i)+".sh")
		cmd := exec.Command("bash", script)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+noGo+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s failed: %s", script, out)
	}

	// Merged results only, no leftovers
	ran := make(map[types.TestKey]int)
	require.NoError(t, filepath.WalkDir(filepath.Join(work, "test-json"), func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			return nil
		}
		assert.Regexp(t, `test-\d+\.jsonl$`, path)
		fp, err := os.Open(path)
		require.NoError(t, err)
		defer fp.Close()
		durations, err := parser.ParseGoTestJSONL(fp)
		require.NoError(t, err)
		for k := range durations.Tests {
			ran[k]++
		}
		return nil
	}))
	assert.Len(t, ran, 12)
	for k, n := range ran {
		assert.Equal(t, 1, n, "%v ran %d times", k, n)
	}

	reports, err := filepath.Glob(filepath.Join(work, "test-reports", "junit-*.xml"))
	require.NoError(t, err)
	assert.NotEmpty(t, reports)
}

// TestBuildCollidingNames builds packages whose `go test -c` binary names collide (a/x and b/x).
func TestBuildCollidingNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	binary := buildBinary(t)
	work := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/m/v2\n\ngo 1.21\n",
		"root_test.go":  "package m\nimport \"testing\"\nfunc TestRoot(t *testing.T) {}\n",
		"a/x/x_test.go": "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n",
		"b/x/x_test.go": "package x\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(work, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	runSplitter(t, binary, work, "", "-s", "-n", "1")

	for bin, test := range map[string]string{"a.x.test": "TestA", "b.x.test": "TestB", "%2E.test": "TestRoot"} {
		out, err := exec.Command(filepath.Join(work, "test-bin", bin), "-test.list", ".").CombinedOutput()
		require.NoError(t, err, "%s: %s", bin, out)
		assert.Equal(t, test+"\n", string(out), bin)
	}
	entries, err := os.ReadDir(filepath.Join(work, "test-bin"))
	require.NoError(t, err)
	assert.Len(t, entries, 4, "test binaries and test2json, no temporary directories are left")
}
