package command

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/takuo/go-testsplitter/internal/types"
)

// Plan is the JSON representation of the split result.
type Plan struct {
	Nodes                  []PlanNode `json:"nodes"`
	TotalTests             int        `json:"total_tests"`
	UnknownTests           int        `json:"unknown_tests"`
	DefaultDurationSeconds float64    `json:"default_duration_seconds"`
	MakespanSeconds        float64    `json:"makespan_seconds"`
}

// PlanNode is the tests assigned to a node.
type PlanNode struct {
	Index            int           `json:"index"`
	EstimatedSeconds float64       `json:"estimated_seconds"`
	Processes        []PlanProcess `json:"processes"`
}

// PlanProcess is a test process invocation in a node.
type PlanProcess struct {
	Package     string     `json:"package"`
	TestPattern string     `json:"test_pattern"`
	Tests       []PlanTest `json:"tests"`
}

// PlanTest is a test function with its estimated duration.
type PlanTest struct {
	Function         string  `json:"function"`
	EstimatedSeconds float64 `json:"estimated_seconds"`
	// Known reports whether the duration comes from previous results.
	Known bool `json:"known"`
}

func (c *CLI) buildPlan() Plan {
	infos := make(map[types.TestKey]types.TestInfo, len(c.testInfos))
	plan := Plan{
		Nodes:                  []PlanNode{},
		TotalTests:             len(c.testInfos),
		DefaultDurationSeconds: c.defaultDuration.Seconds(),
	}
	for _, ti := range c.testInfos {
		infos[ti.TestKey] = ti
		if !ti.Known {
			plan.UnknownTests++
		}
	}

	var makespan time.Duration
	for _, nt := range c.nodeTests {
		node := PlanNode{
			Index:            nt.NodeIndex,
			EstimatedSeconds: nt.TotalDuration.Seconds(),
			Processes:        []PlanProcess{},
		}
		makespan = max(makespan, nt.TotalDuration)

		// Reuse testLines so that the plan matches the generated scripts.
		for _, line := range c.testLines(nt) {
			proc := PlanProcess{Package: line.Package, TestPattern: line.TestPattern}
			for _, fn := range line.Functions {
				ti := infos[types.TestKey{Package: line.Package, Function: fn}]
				proc.Tests = append(proc.Tests, PlanTest{
					Function:         fn,
					EstimatedSeconds: ti.Duration.Seconds(),
					Known:            ti.Known,
				})
			}
			node.Processes = append(node.Processes, proc)
		}
		plan.Nodes = append(plan.Nodes, node)
	}
	plan.MakespanSeconds = makespan.Seconds()
	return plan
}

func (c *CLI) writePlanJSON(path string) (err error) {
	var w io.Writer
	if path == "-" {
		w = c.output()
	} else {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(c.buildPlan())
}

// printPlan prints a human readable summary of the plan.
func (c *CLI) printPlan(w io.Writer) error {
	plan := c.buildPlan()
	seconds := func(s float64) time.Duration {
		return time.Duration(s * float64(time.Second)).Round(time.Millisecond)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tTESTS\tPROCESSES\tESTIMATED")
	for _, n := range plan.Nodes {
		tests := 0
		for _, p := range n.Processes {
			tests += len(p.Tests)
		}
		fmt.Fprintf(tw, "%d\t%d\t%d\t%s\n", n.Index, tests, len(n.Processes), seconds(n.EstimatedSeconds))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nTotal %d tests, makespan %s", plan.TotalTests, seconds(plan.MakespanSeconds))
	if plan.UnknownTests > 0 {
		fmt.Fprintf(w, " (%d tests without previous results, assumed %s each)",
			plan.UnknownTests, seconds(plan.DefaultDurationSeconds))
	}
	fmt.Fprintln(w)

	for _, n := range plan.Nodes {
		fmt.Fprintf(w, "\n[node %d]\n", n.Index)
		for _, p := range n.Processes {
			fmt.Fprintf(w, "  %s %s\n", p.Package, p.TestPattern)
		}
	}
	return nil
}
