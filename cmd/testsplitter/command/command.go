// Package command provides main command line interface
package command

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/alecthomas/kong"
	"github.com/sourcegraph/conc/pool"

	"github.com/takuo/go-testsplitter/internal/parser"
	"github.com/takuo/go-testsplitter/internal/scanner"
	"github.com/takuo/go-testsplitter/internal/templates"
	"github.com/takuo/go-testsplitter/internal/types"
	"github.com/takuo/go-testsplitter/pkg/durchunk"
)

// fallbackDuration is used for tests without previous results when no results are available at all.
const fallbackDuration = 5 * time.Second

// CLI main command line interface
type CLI struct {
	Nodes           int           `short:"n" long:"nodes" default:"4" help:"Number of nodes"`
	Concurrency     int           `short:"c" long:"concurrency" default:"4" help:"Number of concurrent test executions per node"`
	ScriptsDir      string        `short:"o" long:"scripts-dir" default:"./test-scripts" help:"Directory to output generated scripts"`
	ScanPackages    bool          `short:"s" long:"scan-packages" help:"Scan Go packages under the current directory (go list ./...). If not specified, package list (import paths or directories) is read from stdin."`
	Exclude         string        `short:"x" long:"exclude" help:"Regex pattern to exclude packages, matched against import paths and directories"`
	JSONDir         string        `short:"j" long:"json-dir" default:"./test-json" help:"Directory containing go test -json results"`
	Template        string        `short:"t" long:"template" help:"Path to the template file (optional)"`
	MaxFunctions    int           `short:"m" long:"max-functions" default:"0" help:"Maximum number of test functions per test process (0: unlimited)"`
	DefaultDuration time.Duration `long:"default-duration" default:"0s" help:"Duration assumed for tests without previous results (0: median of known durations, or 5s)"`
	Seed            uint64        `long:"seed" default:"1" help:"Random seed for splitting (the same input and seed produce the same scripts)"`
	TestFlags       []string      `arg:"" help:"Flags to pass to the test binary after --" optional:""`

	BinariesDir      string `short:"p" long:"binaries-dir" default:"./test-bin" help:"Directory to output or containing test binaries"`
	BuildConcurrency int    `short:"b" long:"build-concurrency" default:"4" help:"Concurrency for building test binaries"`
	DisableBuild     bool   `short:"d" long:"disable-build" default:"false" help:"Disable building test binaries (use pre-built binaries by other way)"`

	Version kong.VersionFlag `short:"v" long:"version" help:"Print version and exit"`

	// Runtime context
	stdin         io.Reader                       `kong:"-"`
	packages      []scanner.Package               `kong:"-"`
	testFunctions map[string][]string             `kong:"-"`
	testDurations map[types.TestKey]time.Duration `kong:"-"`
	testInfos     []types.TestInfo                `kong:"-"`
	nodeTests     []*types.NodeTest               `kong:"-"`
	template      string                          `kong:"-"`
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
	if c.DefaultDuration < 0 {
		errs = append(errs, errors.New("--default-duration must be >= 0"))
	}
	if c.Exclude != "" {
		if _, err := regexp.Compile(c.Exclude); err != nil {
			errs = append(errs, fmt.Errorf("invalid --exclude pattern: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Run run the command line
func (c *CLI) Run() error {
	if err := c.listPackages(); err != nil {
		return err
	}
	if !c.DisableBuild {
		if err := c.buildTestBinaries(); err != nil {
			return fmt.Errorf("failed to build test binaries: %w", err)
		}
	}
	if err := c.scanTestFunctions(); err != nil {
		return fmt.Errorf("failed to parse test functions: %w", err)
	}

	// Load previous test results
	if err := c.loadTestDurations(); err != nil {
		log.Printf("Warning: Failed to load test durations: %v", err)
	}

	c.createTestInfos()
	c.splitTests()

	if err := c.loadTemplate(); err != nil {
		return fmt.Errorf("failed to load template: %w", err)
	}
	if err := c.generateScriptFiles(); err != nil {
		return fmt.Errorf("failed to generate script files: %w", err)
	}

	fmt.Printf("Generated %d test script files in %s\n", c.Nodes, c.ScriptsDir)
	return nil
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
		log.Printf("Read %d packages from stdin", len(patterns))
	}

	var err error
	if c.packages, err = scanner.ListPackages(patterns, exclude); err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	log.Printf("Found %d packages with test files", len(c.packages))
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

	c.testDurations = make(map[types.TestKey]time.Duration)

	err := filepath.WalkDir(c.JSONDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == c.JSONDir {
				return fs.SkipAll // no previous results
			}
			log.Printf("Skipping %s: %v", path, err)
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

		data, err := parseJSONFile(path)
		if err != nil {
			log.Printf("Failed to read %s: %v", path, err)
		}
		files++
		maps.Copy(c.testDurations, data)
		return nil
	})
	log.Printf("Loaded %d testcases durations from %d files in %s", len(c.testDurations), files, c.JSONDir)
	return err
}

func parseJSONFile(path string) (map[types.TestKey]time.Duration, error) {
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fp.Close()
	return parser.ParseGoTestJSONL(fp)
}

// defaultDuration returns the duration assumed for tests without previous results.
func (c *CLI) defaultDuration() time.Duration {
	if c.DefaultDuration > 0 {
		return c.DefaultDuration
	}
	if len(c.testDurations) == 0 {
		return fallbackDuration
	}
	durations := make([]time.Duration, 0, len(c.testDurations))
	for _, d := range c.testDurations {
		durations = append(durations, d)
	}
	slices.Sort(durations)
	return durations[len(durations)/2]
}

func (c *CLI) createTestInfos() {
	def := c.defaultDuration()
	c.testInfos = nil

	pkgs := slices.Sorted(maps.Keys(c.testFunctions))
	unknown := 0
	for _, pkg := range pkgs {
		for _, fn := range c.testFunctions[pkg] {
			key := types.TestKey{Package: pkg, Function: fn}
			duration, ok := c.testDurations[key]
			if !ok {
				duration = def
				unknown++
			}
			c.testInfos = append(c.testInfos, types.TestInfo{TestKey: key, Duration: duration})
		}
	}
	if unknown > 0 {
		log.Printf("%d of %d tests have no previous results, assuming %s each", unknown, len(c.testInfos), def)
	}
}

func (c *CLI) splitTests() {
	data := func(yield func(types.TestKey, time.Duration) bool) {
		for _, test := range c.testInfos {
			if !yield(test.TestKey, test.Duration) {
				return
			}
		}
	}
	chunks := durchunk.SplitBalanced(data, c.Nodes, durchunk.WithSeed(c.Seed))

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

func (c *CLI) testLines(nt *types.NodeTest) []types.TestLine {
	var lines []types.TestLine
	for _, pkg := range nt.Packages {
		funcs := nt.Funcs[pkg]
		size := c.MaxFunctions
		if size <= 0 {
			size = len(funcs)
		}
		for chunk := range slices.Chunk(funcs, size) {
			lines = append(lines, types.TestLine{
				Index:       len(lines) + 1,
				Package:     pkg,
				TestPattern: "^(" + strings.Join(chunk, "|") + ")$",
			})
		}
	}
	return lines
}

func (c *CLI) generateScriptFiles() error {
	if err := os.MkdirAll(c.ScriptsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	tmpl, err := template.New("test-node.sh").Funcs(templates.FuncMap()).Parse(c.template)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
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
		log.Printf("Generating script: %v (TotalFuncs: %v, TotalDuration: %s)...", filename, numOfFuncs, nt.TotalDuration)

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

// binaryName returns the test binary name for the package directory.
// e.g. api/service/foo → api.service.foo.test
func binaryName(dir string) string {
	return strings.ReplaceAll(dir, "/", ".") + ".test"
}

// buildTestBinaries builds test binaries for all target packages into the binaries directory.
func (c *CLI) buildTestBinaries() error {
	log.Printf("Building test binaries for %d packages with concurrency %d.\n", len(c.packages), c.BuildConcurrency)
	p := pool.New().WithErrors().WithMaxGoroutines(c.BuildConcurrency)

	outputPath, err := filepath.Abs(c.BinariesDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute output path: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}
	for _, pkg := range c.packages {
		p.Go(func() error {
			outputPath := filepath.Join(outputPath, binaryName(pkg.Dir))
			log.Printf("Building %s as %s...\n", pkg.Dir, outputPath)
			cmd := exec.Command("go", "test", "-c", "-o", outputPath, ".")
			cmd.Dir = filepath.Join(cwd, pkg.Dir)
			output, err := cmd.CombinedOutput()
			if len(output) > 0 {
				fmt.Println(string(output))
			}
			return err
		})
	}
	return p.Wait()
}
