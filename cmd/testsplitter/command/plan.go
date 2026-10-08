package command

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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
	// Selection is set with --changed-since.
	Selection *PlanSelection `json:"selection,omitempty"`
}

// PlanSelection is the result of selecting packages by changes.
type PlanSelection struct {
	ChangedSince string   `json:"changed_since"`
	MergeBase    string   `json:"merge_base"`
	ChangedFiles []string `json:"changed_files"`
	// RunAllFile is the changed file which matched --run-all-on, if any. Then all packages are selected.
	RunAllFile      string            `json:"run_all_file,omitempty"`
	ChangedPackages []string          `json:"changed_packages"`
	Selected        []PlanSelectedPkg `json:"selected_packages"`
	TotalPackages   int               `json:"total_packages"`
}

// PlanSelectedPkg is a package selected by changes.
type PlanSelectedPkg struct {
	Package string `json:"package"`
	// AffectedBy is the changed packages (import paths) the tests depend on. Empty when all packages are selected.
	AffectedBy []string `json:"affected_by,omitempty"`
}

// PlanNode is the tests assigned to a node.
type PlanNode struct {
	Index int `json:"index"`
	// EstimatedSeconds is the sum of the estimated durations of the processes.
	EstimatedSeconds float64       `json:"estimated_seconds"`
	Processes        []PlanProcess `json:"processes"`
}

// PlanProcess is a test process invocation in a node, in execution order (longest first).
type PlanProcess struct {
	Package     string `json:"package"`
	Binary      string `json:"binary"`
	TestPattern string `json:"test_pattern"`
	// EstimatedSeconds is the sum of the test durations plus the package overhead.
	EstimatedSeconds float64    `json:"estimated_seconds"`
	OverheadSeconds  float64    `json:"overhead_seconds"`
	Tests            []PlanTest `json:"tests"`
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
		node := PlanNode{Index: nt.NodeIndex, Processes: []PlanProcess{}}
		var total time.Duration

		// Reuse testLines so that the plan matches the generated scripts.
		for _, line := range c.testLines(nt) {
			total += line.Estimated
			proc := PlanProcess{
				Package:          line.Package,
				Binary:           line.Binary,
				TestPattern:      line.TestPattern,
				EstimatedSeconds: line.Estimated.Seconds(),
				OverheadSeconds:  c.overhead(line.Package).Seconds(),
			}
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
		node.EstimatedSeconds = total.Seconds()
		makespan = max(makespan, total)
		plan.Nodes = append(plan.Nodes, node)
	}
	plan.MakespanSeconds = makespan.Seconds()

	if sel := c.selection; sel != nil {
		ps := &PlanSelection{
			ChangedSince:    sel.since,
			MergeBase:       sel.base,
			ChangedFiles:    nonNil(sel.changedFiles),
			RunAllFile:      sel.runAllFile,
			ChangedPackages: nonNil(sel.changedPackages),
			Selected:        []PlanSelectedPkg{},
			TotalPackages:   sel.total,
		}
		for _, pkg := range c.packages {
			ps.Selected = append(ps.Selected, PlanSelectedPkg{Package: pkg.Dir, AffectedBy: sel.affectedBy[pkg.Dir]})
		}
		plan.Selection = ps
	}
	return plan
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
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

	if sel := plan.Selection; sel != nil {
		if sel.RunAllFile != "" {
			fmt.Fprintf(w, "\nRunning all %d packages: %s changed since %s\n", sel.TotalPackages, sel.RunAllFile, sel.ChangedSince)
		} else {
			fmt.Fprintf(w, "\nSelected %d of %d packages affected by %d changed files since %s\n",
				len(sel.Selected), sel.TotalPackages, len(sel.ChangedFiles), sel.ChangedSince)
			for _, p := range sel.Selected {
				fmt.Fprintf(w, "  %s (affected by %s)\n", p.Package, strings.Join(p.AffectedBy, ", "))
			}
		}
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
			fmt.Fprintf(w, "  %-10s %s %s\n", seconds(p.EstimatedSeconds), p.Package, p.TestPattern)
		}
	}
	return nil
}
