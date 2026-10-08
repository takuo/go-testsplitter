package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/takuo/go-testsplitter/internal/scanner"
	"github.com/takuo/go-testsplitter/internal/types"
)

func planCLI() *CLI {
	cli := &CLI{
		Nodes:        2,
		MaxFunctions: 2,
		testFunctions: map[string][]string{
			"pkg1": {"TestA", "TestB", "TestC"},
			"pkg2": {"TestD"},
		},
		testDurations: map[types.TestKey]time.Duration{
			key("pkg1", "TestA"): 4 * time.Second,
			key("pkg1", "TestB"): 1 * time.Second,
			key("pkg2", "TestD"): 3 * time.Second,
		},
	}
	cli.createTestInfos()
	cli.splitTests()
	return cli
}

func TestBuildPlan(t *testing.T) {
	plan := planCLI().buildPlan()

	assert.Equal(t, 4, plan.TotalTests)
	assert.Equal(t, 1, plan.UnknownTests)
	assert.Equal(t, 3.0, plan.DefaultDurationSeconds) // median of 1s, 3s, 4s
	require.Len(t, plan.Nodes, 2)

	seen := make(map[string]PlanTest)
	var maxTotal float64
	for _, n := range plan.Nodes {
		var sum float64
		for _, p := range n.Processes {
			assert.LessOrEqual(t, len(p.Tests), 2, "max functions per process")
			for _, tt := range p.Tests {
				seen[p.Package+":"+tt.Function] = tt
				sum += tt.EstimatedSeconds
			}
		}
		assert.Equal(t, n.EstimatedSeconds, sum)
		maxTotal = max(maxTotal, sum)
	}
	assert.Equal(t, maxTotal, plan.MakespanSeconds)
	assert.Equal(t, PlanTest{Function: "TestA", EstimatedSeconds: 4, Known: true}, seen["pkg1:TestA"])
	assert.Equal(t, PlanTest{Function: "TestC", EstimatedSeconds: 3, Known: false}, seen["pkg1:TestC"])
	assert.Len(t, seen, 4)
}

func TestWritePlanJSON(t *testing.T) {
	cli := planCLI()
	path := filepath.Join(t.TempDir(), "plan.json")
	require.NoError(t, cli.writePlanJSON(path))

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var got Plan
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, cli.buildPlan(), got)

	var stdout bytes.Buffer
	cli.stdout = &stdout
	require.NoError(t, cli.writePlanJSON("-"))
	assert.JSONEq(t, string(b), stdout.String())
}

func TestPrintPlan(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, planCLI().printPlan(&buf))
	out := buf.String()
	assert.Contains(t, out, "NODE  TESTS  PROCESSES  ESTIMATED")
	assert.Contains(t, out, "Total 4 tests, makespan 6s (1 tests without previous results, assumed 3s each)")
	assert.Contains(t, out, "[node 0]")
	assert.Contains(t, out, "[node 1]")
}

func TestPlanSelection(t *testing.T) {
	cli := planCLI()
	cli.packages = []scanner.Package{{Dir: "pkg1"}, {Dir: "pkg2"}}
	cli.selection = &selection{
		since:           "origin/main",
		base:            "abc",
		changedFiles:    []string{"lib/a.go", "pkg2/b.go"},
		changedPackages: []string{"m/lib", "m/pkg2"},
		affectedBy:      map[string][]string{"pkg1": {"m/lib"}, "pkg2": {"m/lib", "m/pkg2"}},
		total:           5,
	}

	plan := cli.buildPlan()
	require.NotNil(t, plan.Selection)
	assert.Equal(t, &PlanSelection{
		ChangedSince:    "origin/main",
		MergeBase:       "abc",
		ChangedFiles:    []string{"lib/a.go", "pkg2/b.go"},
		ChangedPackages: []string{"m/lib", "m/pkg2"},
		Selected: []PlanSelectedPkg{
			{Package: "pkg1", AffectedBy: []string{"m/lib"}},
			{Package: "pkg2", AffectedBy: []string{"m/lib", "m/pkg2"}},
		},
		TotalPackages: 5,
		Granularity:   "package",
	}, plan.Selection)

	var buf bytes.Buffer
	require.NoError(t, cli.printPlan(&buf))
	assert.Contains(t, buf.String(), "Selected 2 of 5 packages affected by 2 changed files since origin/main\n  pkg1 (affected by m/lib)\n")

	cli.selection.runAllFile = "go.mod"
	buf.Reset()
	require.NoError(t, cli.printPlan(&buf))
	assert.Contains(t, buf.String(), "Running all 5 packages: go.mod changed since origin/main")

	cli.selection = nil
	assert.Nil(t, cli.buildPlan().Selection)
}

func TestPlanSelection_Symbol(t *testing.T) {
	cli := planCLI()
	cli.packages = []scanner.Package{{Dir: "pkg1"}, {Dir: "pkg2"}}
	cli.selection = &selection{
		since:          "main",
		changedFiles:   []string{"lib/a.go"},
		total:          3,
		symbol:         true,
		changedSymbols: []string{"m/lib.A"},
		tests:          map[string]map[string]string{"pkg1": {"TestA": "m/lib.A"}},
		allReasons:     map[string]string{"pkg2": "TestMain depends on changed m/lib.A"},
	}
	plan := cli.buildPlan()
	assert.Equal(t, "symbol", plan.Selection.Granularity)
	assert.Equal(t, []string{"m/lib.A"}, plan.Selection.ChangedSymbols)
	assert.Equal(t, []PlanSelectedPkg{
		{Package: "pkg1", Tests: map[string]string{"TestA": "m/lib.A"}},
		{Package: "pkg2", AllTestsReason: "TestMain depends on changed m/lib.A"},
	}, plan.Selection.Selected)

	var buf bytes.Buffer
	require.NoError(t, cli.printPlan(&buf))
	assert.Contains(t, buf.String(), "Changed declarations (1): m/lib.A\n  pkg1: TestA (m/lib.A)\n  pkg2: all tests (TestMain depends on changed m/lib.A)\n")
}
