// Package command provides main command line interface
package command

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io/fs"
	"iter"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/alecthomas/kong"
	"github.com/sourcegraph/conc/pool"
	"github.com/takuo/go-testsplitter/internal/scanner"
	"github.com/takuo/go-testsplitter/internal/templates"
	"github.com/takuo/go-testsplitter/internal/types"
	"github.com/takuo/go-testsplitter/pkg/durchunk"
)

// CLI main command line interface
type CLI struct {
	Nodes        int      `short:"n" long:"nodes" required:"" default:"4" help:"Number of nodes"`
	Concurrency  int      `short:"c" long:"concurrency" default:"4" help:"Number of concurrent test executions per node"`
	ScriptsDir   string   `short:"o" long:"scripts-dir" required:"" default:"./test-scripts" help:"Directory to output generated scripts"`
	ScanPackages bool     `short:"s" long:"scan-packages" help:"Scan Go packages from the current directory (like 'go list'). If not specified, package list is read from stdin."`
	Exclude      string   `short:"x" long:"exclude" help:"Regex pattern to exclude packages (used only with --scan-packages)"`
	ReportDir    string   `short:"r" long:"report-dir" default:"./test-reports" help:"Directory containing JUnit XML test reports"`
	Template     string   `short:"t" long:"template" help:"Path to the template file (optional)"`
	MaxFunctions int      `short:"m" long:"max-functions" default:"0" help:"Maximum number of test functions per package (0: unlimited)"`
	TestFlags    []string `arg:"" help:"Flags to pass to the test binary after --" optional:""`

	BinariesDir      string `short:"p" long:"binaries-dir" default:"./test-bin" help:"Directory to output or containing test binaries"`
	BuildConcurrency int    `short:"b" long:"build-concurrency" default:"4" help:"Concurrency for building test binaries"`
	DisableBuild     bool   `short:"d" long:"disable-build" default:"false" help:"Disable building test binaries (use pre-built binaries by other way)"`

	Version kong.VersionFlag `short:"v" long:"version" help:"Print version and exit"`

	// Runtime context
	packages      []string                  `kong:"-"`
	testFunctions map[string][]string       `kong:"-"`
	testDurations map[string]time.Duration  `kong:"-"`
	testInfos     []types.TestInfo          `kong:"-"`
	nodeTests     iter.Seq[*types.NodeTest] `kong:"-"`
	template      string                    `kong:"-"`
}

func (c *CLI) scanPackages() (err error) {
	// Get packages either from scan directory or stdin
	if c.ScanPackages {
		if c.packages, err = scanner.ScanPackages(c.Exclude); err != nil {
			return fmt.Errorf("failed to scan packages: %v", err)
		}
	} else {
		if err = c.readPackagesFromStdin(); err != nil {
			return fmt.Errorf("failed to read packages from stdin: %v", err)
		}
		log.Printf("Read %d packages from stdin: %v", len(c.packages), c.packages)
	}
	return nil
}

// Run run the command line
func (c *CLI) Run() error {
	if err := c.scanPackages(); err != nil {
		return fmt.Errorf("failed to scan packages from %s: %v", ".", err)
	}
	if !c.DisableBuild {
		if err := c.buildTestBinaries(); err != nil {
			return fmt.Errorf("failed to build test binaries: %w", err)
		}
	}
	// Parse test functions from packages
	if err := c.scanTestFunctions(); err != nil {
		return fmt.Errorf("failed to parse test functions: %w", err)
	}

	// Load previous test results
	if err := c.loadTestDurations(); err != nil {
		log.Printf("Warning: Failed to load test durations: %v", err)
	}

	// Create test info with durations
	c.createTestInfos()

	// Split tests across nodes
	c.splitTests()

	// テンプレートファイルの読み込み（指定があれば）
	if err := c.loadTemplate(); err != nil {
		return fmt.Errorf("failed to load template: %w", err)
	}

	if err := c.generateScriptFiles(); err != nil {
		return fmt.Errorf("failed to generate script files: %w", err)
	}

	fmt.Printf("Generated %d test script files in %s\n", c.Nodes, c.ScriptsDir)
	return nil
}

func (c *CLI) loadTemplate() (err error) {
	if c.Template != "" {
		data, err := os.ReadFile(c.Template)
		if err != nil {
			return fmt.Errorf("failed to read template file: %w", err)
		}
		c.template = string(data)
	} else {
		c.template = templates.ScriptTemplate()
	}
	return
}

func (c *CLI) readPackagesFromStdin() (err error) {
	c.packages = []string{} // initialize packages slice
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		pkg := strings.TrimSpace(scanner.Text())
		if pkg != "" {
			c.packages = append(c.packages, pkg)
		}
	}
	return scanner.Err()
}

func (c *CLI) scanTestFunctions() (err error) {
	c.testFunctions, err = scanner.ScanTestFunctions(c.packages)
	return
}

func (c *CLI) loadTestDurations() (err error) {
	var (
		files int
		cases int
	)

	c.testDurations = make(map[string]time.Duration)

	err = filepath.WalkDir(c.ReportDir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files that can't be accessed
		}

		if !strings.HasSuffix(path, ".xml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Failed to read %s: %v\n", path, err)
			return nil // Skip files that can't be read
		}

		type suites struct {
			XMLName    xml.Name          `xml:"testsuites"`
			TestSuites []types.TestSuite `xml:"testsuite"`
		}
		var xmlData suites
		if err := xml.Unmarshal(data, &xmlData); err != nil {
			log.Printf("Failed to parse XML from %s: %v\n", path, err)
			return nil // Skip files that can't be parsed
		}
		files++
		for _, suite := range xmlData.TestSuites {
			for _, testCase := range suite.TestCases {
				if strings.Contains(testCase.Name, "/") {
					continue
				}
				key := fmt.Sprintf("%s.%s", suite.Name, testCase.Name)
				c.testDurations[key] = time.Duration(testCase.Time * float64(time.Second))
				cases++
			}
		}
		return nil
	})
	log.Printf("Loaded %d testcases durations from %d files in %s\n", cases, files, c.ReportDir)
	return err
}

func (c *CLI) createTestInfos() {
	c.testInfos = []types.TestInfo{}

	for pkg, functions := range c.testFunctions {
		for _, fn := range functions {
			key := fmt.Sprintf("%s.%s", pkg, fn)
			duration := c.testDurations[key]
			if duration == 0 {
				// Default duration for unknown tests
				duration = 5 * time.Second
			}

			c.testInfos = append(c.testInfos, types.TestInfo{
				Package:  pkg,
				Function: fn,
				Duration: duration,
			})
		}
	}
}

func (c *CLI) splitTests() {
	var dataSeq iter.Seq2[string, time.Duration]
	dataSeq = func(yield func(k string, d time.Duration) bool) {
		for _, test := range c.testInfos {
			key := fmt.Sprintf("%s:%s", test.Package, test.Function)
			if !yield(key, test.Duration) {
				return
			}
		}
	}
	chunks := durchunk.SplitBalanced(dataSeq, c.Nodes)
	c.nodeTests = func(yield func(*types.NodeTest) bool) {
		for i, chunk := range chunks {
			nt := &types.NodeTest{
				NodeIndex:     i,
				Funcs:         make(map[string][]string),
				Flags:         strings.Join(c.TestFlags, " "),
				TotalDuration: chunk.Total,
			}
			for _, key := range chunk.Keys {
				s := strings.Index(key, ":")
				pkg, fn := key[:s], key[s+1:]
				nt.Funcs[pkg] = append(nt.Funcs[pkg], fn)
			}
			if !yield(nt) {
				return
			}
		}
	}
}

func (c *CLI) generateScriptFiles() error {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(c.ScriptsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Parse template
	tmpl, err := template.New("test-node.sh").Parse(string(c.template))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	for nt := range c.nodeTests {
		numOfFuncs := 0
		for _, funcs := range nt.Funcs {
			numOfFuncs += len(funcs)
		}
		filename := filepath.Join(c.ScriptsDir, fmt.Sprintf("test-node-%d.sh", nt.NodeIndex))
		log.Printf("Generating script: %v (TotalFuncs: %v, TotalDuration: %s)...\n", filename, numOfFuncs, nt.TotalDuration)

		file, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filename, err)
		}
		defer file.Close()

		// Prepare template data
		linesSeq := func(yield func(tl types.TestLine) bool) {
			for pkg, funcs := range nt.Funcs {
				if c.MaxFunctions > 0 {
					for funcs := range slices.Chunk(funcs, c.MaxFunctions) {
						if !yield(types.TestLine{
							Package:     pkg,
							TestPattern: "^(" + strings.Join(funcs, "|") + ")$",
							Flags:       nt.Flags,
						}) {
							return
						}
					}
				} else {
					if !yield(types.TestLine{
						Package:     pkg,
						TestPattern: "^(" + strings.Join(funcs, "|") + ")$",
						Flags:       nt.Flags,
					}) {
						return
					}
				}
			}
		}

		path, err := filepath.Abs(c.BinariesDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute binary path: %w", err)
		}
		reportDir, err := filepath.Abs(c.ReportDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute report directory: %w", err)
		}
		templateData := types.TemplateData{
			NodeIndex:   nt.NodeIndex,
			Concurrency: c.Concurrency,
			TestLines:   linesSeq,
			Flags:       strings.Join(c.TestFlags, " "),
			ReportDir:   strings.TrimSuffix(reportDir, "/"),
			BinariesDir: strings.TrimSuffix(path, "/"),
		}

		// Execute template
		if err := tmpl.Execute(file, templateData); err != nil {
			return fmt.Errorf("failed to execute template: %w", err)
		}

		// Make the script executable
		if err := file.Chmod(0o755); err != nil {
			return fmt.Errorf("failed to make script executable: %w", err)
		}
	}

	return nil
}

// BuildTestBinaries builds test binaries for all target packages into the output directory.
// The binary name is generated by replacing "/" with "." and appending ".test".
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
	// 例: api/service/foo → api.service.foo.test
	for _, pkg := range c.packages {
		p.Go(func() error {
			binName := strings.ReplaceAll(pkg, "/", ".") + ".test"
			outputPath := filepath.Join(outputPath, binName)
			log.Printf("Building %s as %s...\n", pkg, outputPath)
			outputPath, _ = filepath.Abs(outputPath)
			cmd := exec.Command("go", "test", "-c", "-o", outputPath, ".")
			cmd.Dir = filepath.Join(cwd, pkg)
			output, err := cmd.CombinedOutput()
			if len(output) > 0 {
				fmt.Println(string(output))
			}
			return err
		})
	}
	return p.Wait()
}
