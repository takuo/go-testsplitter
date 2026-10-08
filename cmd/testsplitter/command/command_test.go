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

	"github.com/takuo/go-testsplitter/internal/types"
)

func key(pkg, fn string) types.TestKey {
	return types.TestKey{Package: pkg, Function: fn}
}

func TestValidate(t *testing.T) {
	valid := CLI{Nodes: 1, Concurrency: 1, BuildConcurrency: 1}
	require.NoError(t, valid.Validate())

	cases := map[string]func(c *CLI){
		"nodes":             func(c *CLI) { c.Nodes = 0 },
		"concurrency":       func(c *CLI) { c.Concurrency = 0 },
		"build-concurrency": func(c *CLI) { c.BuildConcurrency = 0 },
		"max-functions":     func(c *CLI) { c.MaxFunctions = -1 },
		"default-duration":  func(c *CLI) { c.DefaultDuration = -time.Second },
		"exclude":           func(c *CLI) { c.Exclude = "(" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid
			mutate(&c)
			assert.ErrorContains(t, c.Validate(), name)
		})
	}
}

func TestCreateTestInfos(t *testing.T) {
	cli := &CLI{
		testFunctions: map[string][]string{
			"pkg2": {"TestC", "TestD"},
			"pkg1": {"TestA", "TestB"},
		},
		testDurations: map[types.TestKey]time.Duration{
			key("pkg1", "TestA"):  10 * time.Second,
			key("pkg2", "TestC"):  0, // known fast test
			key("other", "TestX"): 2 * time.Second,
		},
	}

	cli.createTestInfos()

	// sorted by package, unknown tests get the median of known durations (0, 2s, 10s → 2s)
	assert.Equal(t, []types.TestInfo{
		{TestKey: key("pkg1", "TestA"), Duration: 10 * time.Second},
		{TestKey: key("pkg1", "TestB"), Duration: 2 * time.Second},
		{TestKey: key("pkg2", "TestC"), Duration: 0},
		{TestKey: key("pkg2", "TestD"), Duration: 2 * time.Second},
	}, cli.testInfos)
}

func TestDefaultDuration(t *testing.T) {
	assert.Equal(t, fallbackDuration, (&CLI{}).defaultDuration())
	assert.Equal(t, 3*time.Second, (&CLI{
		DefaultDuration: 3 * time.Second,
		testDurations:   map[types.TestKey]time.Duration{key("p", "T"): time.Second},
	}).defaultDuration())
}

func TestSplitTests(t *testing.T) {
	cli := &CLI{
		Nodes:     2,
		TestFlags: []string{"-test.timeout=20m"},
		testInfos: []types.TestInfo{
			{TestKey: key("pkg1", "TestA"), Duration: 10 * time.Second},
			{TestKey: key("pkg1", "TestB"), Duration: 5 * time.Second},
			{TestKey: key("pkg2", "TestC"), Duration: 15 * time.Second},
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
			{TestKey: key("pkg1", "TestA"), Duration: 10 * time.Second},
			{TestKey: key("pkg1", "TestB"), Duration: 5 * time.Second},
			{TestKey: key("pkg2", "TestC"), Duration: 15 * time.Second},
			{TestKey: key("pkg2", "TestD"), Duration: 7 * time.Second},
		},
	}

	cli.splitTests()

	assert.Len(t, slices.Collect(cli.nodeTests), 2, "Should create 2 nodes (0 origin)")

	// deterministic
	first := slices.Collect(cli.nodeTests)
	cli.splitTests()
	assert.Equal(t, first, slices.Collect(cli.nodeTests))

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
				Packages:  []string{"api/service/foo"},
				Funcs: map[string][]string{
					"api/service/foo": {"TestFoo", "TestBar"},
				},
				Flags: "-test.timeout=20m",
			},
			{
				NodeIndex: 1,
				Packages:  []string{"api/service/bar"},
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
