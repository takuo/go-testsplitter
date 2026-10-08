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
		"max-age":           func(c *CLI) { c.MaxAge = -time.Second },
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
		{TestKey: key("pkg1", "TestA"), Duration: 10 * time.Second, Known: true},
		{TestKey: key("pkg1", "TestB"), Duration: 2 * time.Second},
		{TestKey: key("pkg2", "TestC"), Duration: 0, Known: true},
		{TestKey: key("pkg2", "TestD"), Duration: 2 * time.Second},
	}, cli.testInfos)
}

func TestDefaultDuration(t *testing.T) {
	assert.Equal(t, fallbackDuration, (&CLI{}).estimateDefaultDuration())
	assert.Equal(t, 3*time.Second, (&CLI{
		DefaultDuration: 3 * time.Second,
		testDurations:   map[types.TestKey]time.Duration{key("p", "T"): time.Second},
	}).estimateDefaultDuration())
}

func TestLoadTestDurations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	// newer.jsonl sorts before older.jsonl, but its results are newer
	write("newer.jsonl", `{"Time":"2026-10-01T00:00:00Z","Action":"pass","Package":"p","Test":"TestA","Elapsed":2}
{"Time":"2026-10-01T00:00:01Z","Action":"pass","Package":"p","Elapsed":3}
`)
	write("older.jsonl", `{"Time":"2026-09-01T00:00:00Z","Action":"pass","Package":"p","Test":"TestA","Elapsed":9}
{"Time":"2026-09-01T00:00:00Z","Action":"pass","Package":"p","Test":"TestOld","Elapsed":9}
{"Time":"2026-09-01T00:00:01Z","Action":"pass","Package":"p","Elapsed":28}
`)
	write("notime.jsonl", `{"Action":"pass","Package":"p","Test":"TestA","Elapsed":7}
`)
	now := func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }

	cli := &CLI{JSONDir: dir, now: now}
	require.NoError(t, cli.loadTestDurations())
	assert.Equal(t, map[types.TestKey]time.Duration{
		key("p", "TestA"):   2 * time.Second, // newest wins regardless of file order
		key("p", "TestOld"): 9 * time.Second,
	}, cli.testDurations)

	cli = &CLI{JSONDir: dir, now: now, MaxAge: 30 * 24 * time.Hour}
	require.NoError(t, cli.loadTestDurations())
	assert.Equal(t, map[types.TestKey]time.Duration{key("p", "TestA"): 2 * time.Second}, cli.testDurations)
}

func TestLoadTestDurations_NoDirectory(t *testing.T) {
	cli := &CLI{JSONDir: filepath.Join(t.TempDir(), "missing")}
	require.NoError(t, cli.loadTestDurations())
	assert.Empty(t, cli.testDurations)
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
			"a": {"TestA1", "TestA2", "TestA3", "TestA4"},
			"b": {"TestB1"},
		},
	}
	cli := &CLI{testDurations: map[types.TestKey]time.Duration{
		key("a", "TestA1"): 1 * time.Second,
		key("a", "TestA2"): 2 * time.Second,
		key("a", "TestA3"): 3 * time.Second,
		key("a", "TestA4"): 4 * time.Second,
		key("b", "TestB1"): 5 * time.Second,
	}}

	// longest first
	assert.Equal(t, []types.TestLine{
		{Index: 1, Package: "a", Binary: "a.test", TestPattern: "^(TestA1|TestA2|TestA3|TestA4)$", Functions: []string{"TestA1", "TestA2", "TestA3", "TestA4"}, Estimated: 10 * time.Second},
		{Index: 2, Package: "b", Binary: "b.test", TestPattern: "^(TestB1)$", Functions: []string{"TestB1"}, Estimated: 5 * time.Second},
	}, cli.testLines(nt))

	cli.MaxFunctions = 2
	assert.Equal(t, []types.TestLine{
		{Index: 1, Package: "a", Binary: "a.test", TestPattern: "^(TestA3|TestA4)$", Functions: []string{"TestA3", "TestA4"}, Estimated: 7 * time.Second},
		{Index: 2, Package: "b", Binary: "b.test", TestPattern: "^(TestB1)$", Functions: []string{"TestB1"}, Estimated: 5 * time.Second},
		{Index: 3, Package: "a", Binary: "a.test", TestPattern: "^(TestA1|TestA2)$", Functions: []string{"TestA1", "TestA2"}, Estimated: 3 * time.Second},
	}, cli.testLines(nt))
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
	tmpl, err := cli.parseTemplate()
	require.NoError(t, err)
	require.NoError(t, cli.generateScriptFiles(tmpl))

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
	tmpl, err := cli.parseTemplate()
	require.NoError(t, err)
	require.NoError(t, cli.generateScriptFiles(tmpl))

	content, err := os.ReadFile(filepath.Join(cli.ScriptsDir, "test-node-0.sh"))
	require.NoError(t, err)
	assert.Equal(t, "0:-test.v -test.count=1\n1 'pkg' ^(TestA|TestB)$\n", string(content))
}

func TestGenerateScriptFiles_RemovesStaleScripts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"test-node-0.sh", "test-node-1.sh", "test-node-7.sh", "test-node-x.sh", "other.sh"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o755))
	}
	cli := &CLI{
		Nodes:       2,
		Concurrency: 1,
		ScriptsDir:  dir,
		nodeTests:   []*types.NodeTest{{NodeIndex: 0}, {NodeIndex: 1}},
	}
	tmpl, err := cli.parseTemplate()
	require.NoError(t, err)
	require.NoError(t, cli.generateScriptFiles(tmpl))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"other.sh", "test-node-0.sh", "test-node-1.sh", "test-node-x.sh"}, names)
}
