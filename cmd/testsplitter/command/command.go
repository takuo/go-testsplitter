// Package command provides main command line interface
package command

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/alecthomas/kong"

	"github.com/takuo/go-testsplitter/internal/parser"
	"github.com/takuo/go-testsplitter/internal/scanner"
	"github.com/takuo/go-testsplitter/internal/templates"
	"github.com/takuo/go-testsplitter/internal/types"
	"github.com/takuo/go-testsplitter/pkg/durchunk"
)

// fallbackDuration is used for tests without previous results when no results are available at all.
const fallbackDuration = 5 * time.Second

// scriptNameRe matches generated script file names.
var scriptNameRe = regexp.MustCompile(`^test-node-(\d+)\.sh$`)

// CLI main command line interface
type CLI struct {
	Nodes           int           `short:"n" long:"nodes" default:"4" help:"Number of nodes"`
	Concurrency     int           `short:"c" long:"concurrency" default:"4" help:"Number of concurrent test executions per node"`
	ScriptsDir      string        `short:"o" long:"scripts-dir" default:"./test-scripts" help:"Directory to output generated scripts"`
	ScanPackages    bool          `short:"s" long:"scan-packages" help:"Scan Go packages under the current directory (go list ./...). If not specified, package list (import paths or directories) is read from stdin."`
	Exclude         string        `short:"x" long:"exclude" help:"Regex pattern to exclude packages, matched against import paths and directories"`
	ChangedSince    string        `long:"changed-since" placeholder:"REV" help:"Run only tests of packages affected by changes since the merge base of REV and HEAD (e.g. origin/main)"`
	RunAllOn        string        `long:"run-all-on" placeholder:"REGEX" default:"(^|/)go[.](mod|sum|work)$" help:"With --changed-since, run all tests if a changed file path (relative to the repository root) matches REGEX ('' to disable)"`
	JSONDir         string        `short:"j" long:"json-dir" default:"./test-json" help:"Directory containing go test -json results"`
	MaxAge          time.Duration `long:"max-age" default:"0s" help:"Ignore previous results older than this duration, e.g. 720h (0: no limit)"`
	Template        string        `short:"t" long:"template" help:"Path to the template file (optional)"`
	MaxFunctions    int           `short:"m" long:"max-functions" default:"0" help:"Maximum number of test functions per test process (0: unlimited)"`
	DefaultDuration time.Duration `long:"default-duration" default:"0s" help:"Duration assumed for tests without previous results (0: median of known durations, or 5s)"`
	Seed            uint64        `long:"seed" default:"1" help:"Random seed for splitting (the same input and seed produce the same scripts)"`
	TestFlags       []string      `arg:"" help:"Flags to pass to the test binary after --" optional:""`

	BinariesDir      string `short:"p" long:"binaries-dir" default:"./test-bin" help:"Directory to output or containing test binaries"`
	BuildConcurrency int    `short:"b" long:"build-concurrency" default:"4" help:"Number of packages built in parallel (go test -p)"`
	DisableBuild     bool   `short:"d" long:"disable-build" default:"false" help:"Disable building test binaries (use pre-built binaries by other way)"`

	DryRun bool   `long:"dry-run" help:"Print the split plan without building test binaries or writing scripts"`
	Plan   string `long:"plan" placeholder:"FILE" help:"Write the split plan as JSON to FILE ('-' for stdout)"`

	Quiet   bool `short:"q" long:"quiet" xor:"verbosity" help:"Print only warnings and errors"`
	Verbose bool `long:"verbose" xor:"verbosity" help:"Print debug logs"`

	Version kong.VersionFlag `short:"v" long:"version" help:"Print version and exit"`

	// Runtime context
	stdin           io.Reader                       `kong:"-"`
	stdout          io.Writer                       `kong:"-"`
	patterns        []string                        `kong:"-"`
	packages        []scanner.Package               `kong:"-"`
	selection       *selection                      `kong:"-"`
	testFunctions   map[string][]string             `kong:"-"`
	now             func() time.Time                `kong:"-"`
	testDurations   map[types.TestKey]time.Duration `kong:"-"`
	overheads       map[string]time.Duration        `kong:"-"`
	defaultDuration time.Duration                   `kong:"-"`
	testInfos       []types.TestInfo                `kong:"-"`
	nodeTests       []*types.NodeTest               `kong:"-"`
	template        string                          `kong:"-"`
}

// Validate validates the command line arguments (called by kong).
func (c *CLI) Validate() error {
	var errs []error
	if c.Nodes < 1 {
		errs = append(errs, errors.New("--nodes must be >= 1"))
	}
	if c.Concurrency < 1 {
		errs = append(errs, errors.New("--concurrency must be >= 1"))
	}
	if c.BuildConcurrency < 1 {
		errs = append(errs, errors.New("--build-concurrency must be >= 1"))
	}
	if c.MaxFunctions < 0 {
		errs = append(errs, errors.New("--max-functions must be >= 0"))
	}
	if c.MaxAge < 0 {
		errs = append(errs, errors.New("--max-age must be >= 0"))
	}
	if c.DefaultDuration < 0 {
		errs = append(errs, errors.New("--default-duration must be >= 0"))
	}
	if c.Exclude != "" {
		if _, err := regexp.Compile(c.Exclude); err != nil {
			errs = append(errs, fmt.Errorf("invalid --exclude pattern: %w", err))
		}
	}
	if c.RunAllOn != "" {
		if _, err := regexp.Compile(c.RunAllOn); err != nil {
			errs = append(errs, fmt.Errorf("invalid --run-all-on pattern: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Run run the command line
func (c *CLI) Run(ctx context.Context) error {
	slog.SetDefault(newLogger(os.Stderr, c.logLevel()))

	if err := c.listPackages(); err != nil {
		return err
	}
	if c.ChangedSince != "" {
		if err := c.selectChangedPackages(ctx); err != nil {
			return fmt.Errorf("failed to select packages changed since %s: %w", c.ChangedSince, err)
		}
	}
	if !c.DisableBuild && !c.DryRun {
		if err := c.buildTestBinaries(ctx); err != nil {
			return fmt.Errorf("failed to build test binaries: %w", err)
		}
	}
	if err := c.scanTestFunctions(); err != nil {
		return fmt.Errorf("failed to parse test functions: %w", err)
	}

	// Load previous test results
	if err := c.loadTestDurations(); err != nil {
		slog.Warn("Failed to load test durations", "err", err)
	}

	c.createTestInfos()
	c.splitTests()

	if c.Plan != "" {
		if err := c.writePlanJSON(c.Plan); err != nil {
			return fmt.Errorf("failed to write plan: %w", err)
		}
	}

	// Load and parse the template even in dry-run mode to report errors early
	tmpl, err := c.parseTemplate()
	if err != nil {
		return err
	}
	if c.DryRun {
		if c.Plan == "-" {
			return nil // JSON plan is already written to stdout
		}
		return c.printPlan(c.output())
	}
	if err := c.generateScriptFiles(tmpl); err != nil {
		return fmt.Errorf("failed to generate script files: %w", err)
	}

	slog.Info("Generated test scripts", "nodes", c.Nodes, "dir", c.ScriptsDir)
	return nil
}

func (c *CLI) output() io.Writer {
	if c.stdout != nil {
		return c.stdout
	}
	return os.Stdout
}

func (c *CLI) listPackages() error {
	var exclude *regexp.Regexp
	if c.Exclude != "" {
		exclude = regexp.MustCompile(c.Exclude) // validated in Validate
	}

	patterns := []string{"./..."}
	if !c.ScanPackages {
		var err error
		if patterns, err = c.readPackagesFromStdin(); err != nil {
			return fmt.Errorf("failed to read packages from stdin: %w", err)
		}
		slog.Info("Read packages from stdin", "count", len(patterns))
	}

	c.patterns = patterns
	var err error
	if c.packages, err = scanner.ListPackages(patterns, exclude); err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	slog.Info("Found packages with test files", "count", len(c.packages))
	return nil
}

func (c *CLI) loadTemplate() error {
	if c.Template == "" {
		c.template = templates.ScriptTemplate()
		return nil
	}
	data, err := os.ReadFile(c.Template)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}
	c.template = string(data)
	return nil
}

func (c *CLI) parseTemplate() (*template.Template, error) {
	if err := c.loadTemplate(); err != nil {
		return nil, fmt.Errorf("failed to load template: %w", err)
	}
	tmpl, err := template.New("test-node.sh").Funcs(templates.FuncMap()).Parse(c.template)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	return tmpl, nil
}

func (c *CLI) readPackagesFromStdin() ([]string, error) {
	r := c.stdin
	if r == nil {
		r = os.Stdin
	}
	var packages []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if pkg := strings.TrimSpace(sc.Text()); pkg != "" {
			packages = append(packages, pkg)
		}
	}
	return packages, sc.Err()
}

func (c *CLI) scanTestFunctions() (err error) {
	c.testFunctions, err = scanner.ScanTestFunctions(c.packages)
	return err
}

func (c *CLI) loadTestDurations() error {
	var files int

	tests := make(map[types.TestKey]parser.Result)
	overheads := make(map[string][]time.Duration)
	c.testDurations = make(map[types.TestKey]time.Duration)
	c.overheads = make(map[string]time.Duration)

	var since time.Time
	if c.MaxAge > 0 {
		now := time.Now
		if c.now != nil {
			now = c.now
		}
		since = now().Add(-c.MaxAge)
	}
	fresh := func(r parser.Result) bool {
		return since.IsZero() || r.Time.IsZero() || !r.Time.Before(since)
	}

	err := filepath.WalkDir(c.JSONDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == c.JSONDir {
				return fs.SkipAll // no previous results
			}
			slog.Warn("Skipping unreadable path", "path", path, "err", err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".jsonl", ".json":
		default:
			return nil
		}

		results, err := parseJSONFile(path)
		if err != nil {
			slog.Warn("Failed to read test results", "path", path, "err", err)
			if results == nil {
				return nil
			}
		}
		files++
		for k, r := range results.Tests {
			// The newest result wins. Results without time are older than any timed result.
			if prev, ok := tests[k]; fresh(r) && (!ok || !r.Time.Before(prev.Time)) {
				tests[k] = r
			}
		}
		for pkg, samples := range results.Overheads {
			for _, r := range samples {
				if fresh(r) {
					overheads[pkg] = append(overheads[pkg], r.Duration)
				}
			}
		}
		return nil
	})
	for k, r := range tests {
		c.testDurations[k] = r.Duration
	}
	for pkg, samples := range overheads {
		c.overheads[pkg] = median(samples)
	}
	slog.Info("Loaded test durations", "tests", len(c.testDurations), "files", files, "dir", c.JSONDir)
	slog.Debug("Estimated package overheads", "packages", len(c.overheads))
	return err
}

// median returns the median of durations. It sorts durations in place.
func median(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	slices.Sort(durations)
	return durations[len(durations)/2]
}

func parseJSONFile(path string) (*parser.Results, error) {
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fp.Close()
	return parser.ParseGoTestJSONL(fp)
}

// estimateDefaultDuration returns the duration assumed for tests without previous results.
func (c *CLI) estimateDefaultDuration() time.Duration {
	if c.DefaultDuration > 0 {
		return c.DefaultDuration
	}
	if len(c.testDurations) == 0 {
		return fallbackDuration
	}
	return median(slices.Collect(maps.Values(c.testDurations)))
}

func (c *CLI) createTestInfos() {
	c.defaultDuration = c.estimateDefaultDuration()
	c.testInfos = nil

	unknown := 0
	for _, pkg := range slices.Sorted(maps.Keys(c.testFunctions)) {
		for _, fn := range c.testFunctions[pkg] {
			key := types.TestKey{Package: pkg, Function: fn}
			duration, ok := c.testDurations[key]
			if !ok {
				duration = c.defaultDuration
				unknown++
			}
			c.testInfos = append(c.testInfos, types.TestInfo{TestKey: key, Duration: duration, Known: ok})
		}
	}
	if unknown > 0 {
		slog.Info("Some tests have no previous results", "unknown", unknown, "total", len(c.testInfos), "assumed", c.defaultDuration)
	}
}

// splitTests splits tests across nodes. The estimated overhead of a package is added once to
// each node running the package, which favors keeping tests of a costly package together.
func (c *CLI) splitTests() {
	c.warnLongTests()

	items := make([]durchunk.Item[types.TestKey], 0, len(c.testInfos))
	for _, test := range c.testInfos {
		items = append(items, durchunk.Item[types.TestKey]{Key: test.TestKey, Duration: test.Duration, Group: test.Package})
	}
	chunks := durchunk.SplitBalancedItems(items, c.Nodes, c.overhead, durchunk.WithSeed(c.Seed))

	c.nodeTests = make([]*types.NodeTest, 0, len(chunks))
	for i, chunk := range chunks {
		nt := &types.NodeTest{
			NodeIndex:     i,
			Funcs:         make(map[string][]string),
			TotalDuration: chunk.Total,
		}
		for _, key := range chunk.Keys {
			if _, ok := nt.Funcs[key.Package]; !ok {
				nt.Packages = append(nt.Packages, key.Package)
			}
			nt.Funcs[key.Package] = append(nt.Funcs[key.Package], key.Function)
		}
		c.nodeTests = append(c.nodeTests, nt)
	}
}

// overhead returns the estimated per-process overhead of the package.
func (c *CLI) overhead(pkg string) time.Duration {
	return c.overheads[pkg]
}

// warnLongTests warns about tests longer than the ideal time per node,
// which limit how evenly tests can be split.
func (c *CLI) warnLongTests() {
	var total time.Duration
	for _, ti := range c.testInfos {
		total += ti.Duration
	}
	for _, d := range c.overheads {
		total += d
	}
	ideal := total / time.Duration(c.Nodes)

	var long []types.TestInfo
	for _, ti := range c.testInfos {
		if ti.Known && ti.Duration > ideal {
			long = append(long, ti)
		}
	}
	slices.SortStableFunc(long, func(a, b types.TestInfo) int { return cmp.Compare(b.Duration, a.Duration) })
	for i, ti := range long {
		if i == 5 {
			slog.Warn("More tests are longer than the ideal time per node", "count", len(long)-i)
			break
		}
		slog.Warn("Test is longer than the ideal time per node; consider splitting it",
			"package", ti.Package, "test", ti.Function, "duration", ti.Duration, "ideal", ideal)
	}
}

// testLines returns test process invocations of the node, longest first so that
// long processes do not start last when running in parallel.
func (c *CLI) testLines(nt *types.NodeTest) []types.TestLine {
	var lines []types.TestLine
	for _, pkg := range nt.Packages {
		for _, funcs := range c.groupFunctions(pkg, nt.Funcs[pkg]) {
			estimated := c.overhead(pkg)
			for _, fn := range funcs {
				estimated += c.testDuration(types.TestKey{Package: pkg, Function: fn})
			}
			lines = append(lines, types.TestLine{
				Package:     pkg,
				Binary:      binaryName(pkg),
				TestPattern: "^(" + strings.Join(funcs, "|") + ")$",
				Functions:   funcs,
				Estimated:   estimated,
			})
		}
	}
	slices.SortStableFunc(lines, func(a, b types.TestLine) int { return cmp.Compare(b.Estimated, a.Estimated) })
	for i := range lines {
		lines[i].Index = i + 1
	}
	return lines
}

// groupFunctions splits functions of a package into ceil(n / MaxFunctions) processes
// with balanced durations (LPT with capacity). Functions in each process are sorted by name.
func (c *CLI) groupFunctions(pkg string, funcs []string) [][]string {
	if c.MaxFunctions <= 0 || len(funcs) <= c.MaxFunctions {
		return [][]string{funcs}
	}
	type fnDur struct {
		name string
		dur  time.Duration
	}
	sorted := make([]fnDur, 0, len(funcs))
	for _, fn := range funcs {
		sorted = append(sorted, fnDur{fn, c.testDuration(types.TestKey{Package: pkg, Function: fn})})
	}
	slices.SortStableFunc(sorted, func(a, b fnDur) int { return cmp.Compare(b.dur, a.dur) })

	groups := make([][]string, (len(funcs)+c.MaxFunctions-1)/c.MaxFunctions)
	sums := make([]time.Duration, len(groups))
	for _, f := range sorted {
		best := -1
		for i := range groups {
			if len(groups[i]) < c.MaxFunctions && (best < 0 || sums[i] < sums[best]) {
				best = i
			}
		}
		groups[best] = append(groups[best], f.name)
		sums[best] += f.dur
	}
	for _, g := range groups {
		slices.Sort(g)
	}
	return groups
}

// testDuration returns the estimated duration of the test.
func (c *CLI) testDuration(key types.TestKey) time.Duration {
	if d, ok := c.testDurations[key]; ok {
		return d
	}
	return c.defaultDuration
}

func (c *CLI) generateScriptFiles(tmpl *template.Template) error {
	if err := os.MkdirAll(c.ScriptsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	if err := c.removeStaleScripts(); err != nil {
		return err
	}

	binariesDir, err := filepath.Abs(c.BinariesDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute binary path: %w", err)
	}
	jsonDir, err := filepath.Abs(c.JSONDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute JSON directory: %w", err)
	}

	for _, nt := range c.nodeTests {
		numOfFuncs := 0
		for _, funcs := range nt.Funcs {
			numOfFuncs += len(funcs)
		}
		filename := filepath.Join(c.ScriptsDir, fmt.Sprintf("test-node-%d.sh", nt.NodeIndex))
		slog.Info("Generating script", "file", filename, "tests", numOfFuncs, "estimated", nt.TotalDuration)

		data := types.TemplateData{
			NodeIndex:   nt.NodeIndex,
			Concurrency: c.Concurrency,
			TestLines:   c.testLines(nt),
			Flags:       strings.Join(c.TestFlags, " "),
			TestFlags:   c.TestFlags,
			JSONDir:     jsonDir,
			BinariesDir: binariesDir,
		}
		if err := writeScript(filename, tmpl, data); err != nil {
			return err
		}
	}
	return nil
}

// removeStaleScripts removes scripts for node indexes which are no longer generated
// (e.g. test-node-7.sh after reducing --nodes from 8 to 4).
func (c *CLI) removeStaleScripts() error {
	entries, err := os.ReadDir(c.ScriptsDir)
	if err != nil {
		return fmt.Errorf("failed to read scripts directory: %w", err)
	}
	for _, e := range entries {
		m := scriptNameRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		if idx, err := strconv.Atoi(m[1]); err == nil && idx < c.Nodes {
			continue
		}
		path := filepath.Join(c.ScriptsDir, e.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove stale script %s: %w", path, err)
		}
		slog.Info("Removed stale script", "file", path)
	}
	return nil
}

func writeScript(filename string, tmpl *template.Template, data types.TemplateData) (err error) {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close %s: %w", filename, cerr)
		}
	}()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute template for %s: %w", filename, err)
	}
	// Ensure executable even if the file already existed with other permissions
	if err := file.Chmod(0o755); err != nil {
		return fmt.Errorf("failed to make %s executable: %w", filename, err)
	}
	return nil
}
