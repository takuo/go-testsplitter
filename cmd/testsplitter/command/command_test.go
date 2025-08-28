package command

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/takuo/go-testsplitter/internal/scanner"
	"github.com/takuo/go-testsplitter/internal/types"
)

func TestParseTestFunctions(t *testing.T) {
	// Create a temporary test directory
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "example_test.go")

	// Create a sample test file
	testContent := `package example

import "testing"

func TestExample(t *testing.T) {
	// Test implementation
}

func TestAnother(t *testing.T) {
	// Another test implementation
}

func BenchmarkExample(b *testing.B) {
	// Benchmark - should be ignored
}

func helperFunction() {
	// Helper function - should be ignored
}
`

	err := os.WriteFile(testFile, []byte(testContent), 0o644)
	require.NoError(t, err, "Failed to create test file")

	testFunctions, err := scanner.ScanTestFunctions([]string{tmpDir})
	require.NoError(t, err, "parseTestFunctions should not fail")

	functions, exists := testFunctions[tmpDir]
	require.True(t, exists, "Expected package %s to be found", tmpDir)

	expectedFunctions := []string{"TestExample", "TestAnother"}
	assert.Equal(t, len(expectedFunctions), len(functions), "Should have correct number of test functions")
	assert.Contains(t, functions, "TestExample", "Should contain TestExample")
	assert.Contains(t, functions, "TestAnother", "Should contain TestAnother")
}

func TestCreateTestInfos(t *testing.T) {
	cli := &CLI{
		testFunctions: map[string][]string{
			"pkg1": {"TestA", "TestB"},
			"pkg2": {"TestC"},
		},
		testDurations: map[string]time.Duration{
			"pkg1:TestA": 10 * time.Second,
			"pkg2:TestC": 5 * time.Second,
		},
	}

	cli.createTestInfos()

	assert.Len(t, cli.testInfos, 3, "Should have 3 test infos")

	// Check that known duration is used
	for _, info := range cli.testInfos {
		if info.Package == "pkg1" && info.Function == "TestA" {
			assert.Equal(t, 10*time.Second, info.Duration, "Should use known duration for TestA")
		}
		if info.Package == "pkg1" && info.Function == "TestB" {
			assert.Equal(t, 5*time.Second, info.Duration, "Should use default duration for TestB")
		}
	}
}

func TestSplitTests(t *testing.T) {
	cli := &CLI{
		Nodes:     2,
		TestFlags: []string{"-test.timeout=20m"},
		testInfos: []types.TestInfo{
			{Package: "pkg1", Function: "TestA", Duration: 10 * time.Second},
			{Package: "pkg1", Function: "TestB", Duration: 5 * time.Second},
			{Package: "pkg2", Function: "TestC", Duration: 15 * time.Second},
		},
	}

	cli.splitTests()

	assert.Len(t, slices.Collect(cli.nodeTests), 2, "Should create 2 nodes")

	// Check that all packages are assigned
	allPackages := make(map[string]bool)
	for nt := range cli.nodeTests {
		for pkg := range nt.Funcs {
			allPackages[pkg] = true
		}
	}

	assert.True(t, allPackages["pkg1"], "pkg1 should be assigned to a node")
	assert.True(t, allPackages["pkg2"], "pkg2 should be assigned to a node")
}

func TestSplitTests_FunctionLevel(t *testing.T) {
	cli := &CLI{
		Nodes:     2,
		TestFlags: []string{"-test.timeout=20m"},
		testInfos: []types.TestInfo{
			{Package: "pkg1", Function: "TestA", Duration: 10 * time.Second},
			{Package: "pkg1", Function: "TestB", Duration: 5 * time.Second},
			{Package: "pkg2", Function: "TestC", Duration: 15 * time.Second},
			{Package: "pkg2", Function: "TestD", Duration: 7 * time.Second},
		},
	}

	cli.splitTests()

	assert.Len(t, slices.Collect(cli.nodeTests), 2, "Should create 2 nodes (0 origin)")

	// 各関数がどこか1つのノードにしか割り当てられていないこと
	funcSet := make(map[string]struct{})
	for nt := range cli.nodeTests {
		for pkg, fns := range nt.Funcs {
			for _, fn := range fns {
				key := pkg + ":" + fn
				if _, exists := funcSet[key]; exists {
					t.Errorf("Function %s assigned to multiple nodes", key)
				}
				funcSet[key] = struct{}{}
			}
		}
	}
	assert.Equal(t, 4, len(funcSet), "All 4 functions should be assigned exactly once")

	// ノードごとにパッケージが重複してもよいが、関数は重複しない
	for nt := range cli.nodeTests {
		seen := make(map[string]struct{})
		for pkg, fns := range nt.Funcs {
			for _, fn := range fns {
				key := pkg + ":" + fn
				if _, ok := seen[key]; ok {
					t.Errorf("Duplicate function %s in one node", key)
				}
				seen[key] = struct{}{}
			}
		}
	}

	// ノードごとにArgsが正しく設定されているか
	for nt := range cli.nodeTests {
		for range nt.Funcs {
			assert.Equal(t, "-test.timeout=20m", nt.Flags)
		}
	}

	// 各ノードの合計テスト時間が近いことを検証
	totals := make([]time.Duration, cli.Nodes)
	for nt := range cli.nodeTests {
		for pkg, fns := range nt.Funcs {
			for _, fn := range fns {
				for _, ti := range cli.testInfos {
					if ti.Package == pkg && ti.Function == fn {
						totals[nt.NodeIndex] += ti.Duration
					}
				}
			}
		}
	}
	min, max := totals[0], totals[0]
	for _, v := range totals[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	// 最大と最小の差が最大のテスト関数時間以下であれば「近い」とみなす
	var maxSingle time.Duration
	for _, ti := range cli.testInfos {
		if ti.Duration > maxSingle {
			maxSingle = ti.Duration
		}
	}
	assert.LessOrEqual(t, max-min, maxSingle, "Node total durations should be balanced (diff=%v, maxSingle=%v)", max-min, maxSingle)
}

func TestGenerateScriptFiles(t *testing.T) {
	cli := &CLI{
		Nodes:      2,
		ScriptsDir: t.TempDir(),
		nodeTests: slices.Values([]*types.NodeTest{
			{
				NodeIndex: 0,
				Funcs: map[string][]string{
					"api/service/foo": {"TestFoo", "TestBar"},
				},
				Flags: "-test.timeout=20m",
			},
			{
				NodeIndex: 1,
				Funcs: map[string][]string{
					"api/service/bar": {"TestBaz"},
				},
				Flags: "-test.timeout=20m",
			},
		}),
	}
	cli.loadTemplate()

	// Use the default template content as in main.go
	require.NoError(t, cli.generateScriptFiles(), "generateScriptFiles should not fail")

	// Check that script files were created
	for i := range cli.Nodes {
		scriptPath := filepath.Join(cli.ScriptsDir, "test-node-"+strconv.Itoa(i)+".sh")
		assert.FileExists(t, scriptPath, "Script file should exist")

		// Check script content
		content, err := os.ReadFile(scriptPath)
		require.NoError(t, err, "Should be able to read script file")

		contentStr := string(content)
		assert.Contains(t, contentStr, "#!/bin/bash", "Script should contain shebang")
		assert.Contains(t, contentStr, "set -e", "Script should contain set -e")
	}
}
