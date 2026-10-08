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
