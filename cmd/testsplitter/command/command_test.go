package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
		Nodes: 2,
		testInfos: []types.TestInfo{
			{TestKey: key("pkg1", "TestA"), Duration: 10 * time.Second},
			{TestKey: key("pkg1", "TestB"), Duration: 5 * time.Second},
			{TestKey: key("pkg2", "TestC"), Duration: 15 * time.Second},
			{TestKey: key("pkg2", "TestD"), Duration: 7 * time.Second},
		},
	}

	cli.splitTests()
	require.Len(t, cli.nodeTests, 2)

	// every function is assigned exactly once, and totals are consistent
	assigned := make(map[types.TestKey]int)
	totals := make([]time.Duration, cli.Nodes)
	for _, nt := range cli.nodeTests {
		assert.Len(t, nt.Packages, len(nt.Funcs))
		for _, pkg := range nt.Packages {
			for _, fn := range nt.Funcs[pkg] {
				assigned[key(pkg, fn)]++
				for _, ti := range cli.testInfos {
					if ti.TestKey == key(pkg, fn) {
						totals[nt.NodeIndex] += ti.Duration
					}
				}
			}
		}
		assert.Equal(t, totals[nt.NodeIndex], nt.TotalDuration)
	}
	for _, ti := range cli.testInfos {
		assert.Equal(t, 1, assigned[ti.TestKey], "%v", ti.TestKey)
	}
	// optimal makespan: {15, 5} / {10, 7} → 20s
	assert.Equal(t, 20*time.Second, max(totals[0], totals[1]))

	// deterministic
	first := cli.nodeTests
	cli.splitTests()
	assert.Equal(t, first, cli.nodeTests)
}

func TestTestLines(t *testing.T) {
	nt := &types.NodeTest{
		Packages: []string{"b", "a"},
		Funcs: map[string][]string{
			"a": {"TestA1", "TestA2", "TestA3"},
			"b": {"TestB1"},
		},
	}
	assert.Equal(t, []types.TestLine{
		{Index: 1, Package: "b", TestPattern: "^(TestB1)$"},
		{Index: 2, Package: "a", TestPattern: "^(TestA1|TestA2|TestA3)$"},
	}, (&CLI{}).testLines(nt))

	assert.Equal(t, []types.TestLine{
		{Index: 1, Package: "b", TestPattern: "^(TestB1)$"},
		{Index: 2, Package: "a", TestPattern: "^(TestA1|TestA2)$"},
		{Index: 3, Package: "a", TestPattern: "^(TestA3)$"},
	}, (&CLI{MaxFunctions: 2}).testLines(nt))
}

func TestGenerateScriptFiles(t *testing.T) {
	cli := &CLI{
		Nodes:       3,
		Concurrency: 2,
		ScriptsDir:  t.TempDir(),
		TestFlags:   []string{"-test.timeout=20m", "-test.run=it's quoted"},
		nodeTests: []*types.NodeTest{
			{
				NodeIndex: 0,
				Packages:  []string{"api/service/foo"},
				Funcs:     map[string][]string{"api/service/foo": {"TestFoo", "TestBar"}},
			},
			{
				NodeIndex: 1,
				Packages:  []string{"api/service/bar"},
				Funcs:     map[string][]string{"api/service/bar": {"TestBaz"}},
			},
			{NodeIndex: 2, Funcs: map[string][]string{}}, // more nodes than tests
		},
	}
	require.NoError(t, cli.loadTemplate())
	require.NoError(t, cli.generateScriptFiles())

	for i := range cli.Nodes {
		scriptPath := filepath.Join(cli.ScriptsDir, "test-node-"+strconv.Itoa(i)+".sh")
		fi, err := os.Stat(scriptPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())

		content, err := os.ReadFile(scriptPath)
		require.NoError(t, err)
		contentStr := string(content)
		assert.True(t, strings.HasPrefix(contentStr, "#!/bin/bash\n"))
		assert.Contains(t, contentStr, `local flags=('-test.timeout=20m' '-test.run=it'\''s quoted' )`)

		// the generated script is valid bash
		out, err := exec.Command("bash", "-n", scriptPath).CombinedOutput()
		assert.NoError(t, err, "%s", out)
	}
}

func TestGenerateScriptFiles_CustomTemplate(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "custom.tmpl")
	require.NoError(t, os.WriteFile(tmplPath, []byte(
		"{{.NodeIndex}}:{{.Flags}}{{range .TestLines}}\n{{.Index}} {{shquote .Package}} {{.TestPattern}}{{end}}\n"), 0o644))

	cli := &CLI{
		Nodes:      1,
		ScriptsDir: filepath.Join(dir, "out"),
		Template:   tmplPath,
		TestFlags:  []string{"-test.v", "-test.count=1"},
		nodeTests: []*types.NodeTest{{
			Packages: []string{"pkg"},
			Funcs:    map[string][]string{"pkg": {"TestA", "TestB"}},
		}},
	}
	require.NoError(t, cli.loadTemplate())
	require.NoError(t, cli.generateScriptFiles())

	content, err := os.ReadFile(filepath.Join(cli.ScriptsDir, "test-node-0.sh"))
	require.NoError(t, err)
	assert.Equal(t, "0:-test.v -test.count=1\n1 'pkg' ^(TestA|TestB)$\n", string(content))
}
