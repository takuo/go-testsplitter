package impact

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/takuo/go-testsplitter/internal/gitdiff"
	"github.com/takuo/go-testsplitter/internal/scanner"
)

const mod = "example.com/m"

// baseFiles is a module where most packages import "constant".
var baseFiles = map[string]string{
	"go.mod": "module example.com/m\n\ngo 1.21\n",
	"constant/constant.go": `package constant

const A = 1

const B = 2

const (
	X = iota
	Y
)
`,
	"usea/usea.go": `package usea

import "example.com/m/constant"

// UseA returns A.
func UseA() int { return constant.A }

func Plain() int { return 0 }
`,
	"usea/usea_test.go": `package usea

import "testing"

func TestUseA(t *testing.T) { UseA() }
func TestPlain(t *testing.T) { Plain() }
`,
	"useb/useb.go": `package useb

import "example.com/m/constant"

func UseB() int { return constant.B }
func UseY() int { return constant.Y }
`,
	"useb/useb_test.go": `package useb_test

import (
	"testing"

	"example.com/m/useb"
)

func TestUseB(t *testing.T) { useb.UseB() }
func TestUseY(t *testing.T) { useb.UseY() }
func TestHelper(t *testing.T) { helper() }
func helper() {}
`,
	"shape/shape.go": `package shape

import "example.com/m/constant"

type Shape interface{ area() int }

type square struct{}

func (square) area() int { return constant.A }

func NewSquare() Shape { return square{} }

func Total(s Shape) int { return s.area() }

type Service struct{}

func (Service) Do() int { return 1 }

func NewService() Service { return Service{} }
`,
	"shape/shape_test.go": `package shape

import "testing"

func TestTotal(t *testing.T) { Total(NewSquare()) }
func TestService(t *testing.T) { NewService() }
func TestNothing(t *testing.T) {}
`,
	"reg/reg.go": `package reg

var registry = map[string]func() int{}

func Register(name string, f func() int) { registry[name] = f }

func Call(name string) int { return registry[name]() }

type impl struct{}

func (impl) value() int { return 1 }

func init() { Register("impl", impl{}.value) }
`,
	"reg/reg_test.go": `package reg

import "testing"

func TestCall(t *testing.T) { Call("impl") }
`,
	"withmain/withmain_test.go": `package withmain

import (
	"os"
	"testing"

	"example.com/m/constant"
)

func TestMain(m *testing.M) { _ = constant.B; os.Exit(m.Run()) }
func TestW(t *testing.T) {}
`,
	"fixture/fixture_test.go": `package fixture

import "testing"

func TestFixture(t *testing.T) {}
`,
	"fixture/testdata/a.txt": "a\n",
	"cmd/tool/main.go": `package main

import "example.com/m/usea"

func main() { println(usea.UseA()) }
`,
	"cmd/tool/main_test.go": `package main

import "testing"

func TestBuildAndRun(t *testing.T) {} // e.g. runs "go build" and the binary
func TestOther(t *testing.T) {}
`,
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if content == "" {
			require.NoError(t, os.Remove(path))
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// analyze commits baseFiles, applies changes (an empty content removes the file) and analyzes them.
func analyze(t *testing.T, changes map[string]string) *Analysis {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, baseFiles)
	git(t, dir, "init")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	writeFiles(t, dir, changes)
	t.Chdir(dir)

	ctx := context.Background()
	c, err := gitdiff.ChangedSince(ctx, dir, "HEAD")
	require.NoError(t, err)
	graph, err := scanner.LoadDepGraph([]string{"./..."})
	require.NoError(t, err)

	res, err := Analyze(ctx, Options{Root: c.Root, Base: c.Base, Files: c.Files, Graph: graph, Candidates: graph.MainPackages()})
	require.NoError(t, err)
	return res
}

// selected summarizes selections: package (without the module path) → sorted test functions, or "ALL".
func selected(res *Analysis) map[string][]string {
	out := make(map[string][]string)
	for pkg, sel := range res.Selections {
		short := pkg[len(mod)+1:]
		if sel.All {
			out[short] = []string{"ALL"}
			continue
		}
		for fn := range sel.Functions {
			out[short] = append(out[short], fn)
		}
		slices.Sort(out[short])
	}
	return out
}

func TestAnalyze(t *testing.T) {
	cases := map[string]struct {
		changes map[string]string
		want    map[string][]string
		symbols []string
	}{
		"adding a constant selects nothing": {
			changes: map[string]string{"constant/constant.go": baseFiles["constant/constant.go"] + "\nconst C = 3\n"},
			want:    map[string][]string{},
			symbols: []string{mod + "/constant.C"},
		},
		"changing a constant selects its users only": {
			changes: map[string]string{"constant/constant.go": replace(baseFiles["constant/constant.go"], "const A = 1", "const A = 10")},
			want: map[string][]string{
				"usea":     {"TestUseA"},
				"shape":    {"TestTotal"}, // square.area uses A, reached via NewSquare
				"cmd/tool": {"ALL"},       // main depends on A
			},
		},
		"comment and formatting changes select nothing": {
			changes: map[string]string{"usea/usea.go": replace(baseFiles["usea/usea.go"], "// UseA returns A.\nfunc UseA() int { return constant.A }", "// UseA returns constant A.\nfunc UseA() int {\n\treturn constant.A\n}")},
			want:    map[string][]string{},
		},
		"iota group changes affect all names in the group": {
			changes: map[string]string{"constant/constant.go": replace(baseFiles["constant/constant.go"], "X = iota", "W = iota\n\tX")},
			want:    map[string][]string{"useb": {"TestUseY"}},
		},
		"unexported method change reaches callers through interfaces": {
			changes: map[string]string{"shape/shape.go": replace(baseFiles["shape/shape.go"], "func (square) area() int { return constant.A }", "func (square) area() int { return constant.A + 1 }")},
			want:    map[string][]string{"shape": {"TestTotal"}},
		},
		"exported method change is treated as a change of the type": {
			changes: map[string]string{"shape/shape.go": replace(baseFiles["shape/shape.go"], "func (Service) Do() int { return 1 }", "func (Service) Do() int { return 2 }")},
			want:    map[string][]string{"shape": {"TestService"}},
		},
		"code used by init falls back to the package": {
			changes: map[string]string{"reg/reg.go": replace(baseFiles["reg/reg.go"], "func (impl) value() int { return 1 }", "func (impl) value() int { return 2 }")},
			want:    map[string][]string{"reg": {"ALL"}},
		},
		"TestMain dependency selects all tests of the package": {
			changes: map[string]string{"constant/constant.go": replace(baseFiles["constant/constant.go"], "const B = 2", "const B = 20")},
			want: map[string][]string{
				"useb":     {"TestUseB"},
				"withmain": {"ALL"},
			},
		},
		"test helper change selects its tests": {
			changes: map[string]string{"useb/useb_test.go": replace(baseFiles["useb/useb_test.go"], "func helper() {}", "func helper() { _ = 1 }")},
			want:    map[string][]string{"useb": {"TestHelper"}},
		},
		"new test is selected": {
			changes: map[string]string{"usea/new_test.go": "package usea\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {}\n"},
			want:    map[string][]string{"usea": {"TestNew"}},
		},
		"testdata change falls back to the package": {
			changes: map[string]string{"fixture/testdata/a.txt": "b\n"},
			want:    map[string][]string{"fixture": {"ALL"}},
		},
		"build constraint change falls back to the package": {
			changes: map[string]string{"usea/usea.go": "//go:build !never\n\n" + baseFiles["usea/usea.go"]},
			want:    map[string][]string{"usea": {"ALL"}, "cmd/tool": {"ALL"}}, // all tests linking usea
		},
		"moving a declaration between files selects nothing": {
			changes: map[string]string{
				"usea/usea.go":  replace(baseFiles["usea/usea.go"], "func Plain() int { return 0 }\n", ""),
				"usea/plain.go": "package usea\n\nfunc Plain() int { return 0 }\n",
			},
			want: map[string][]string{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := analyze(t, tc.changes)
			assert.Equal(t, tc.want, selected(res), "fallbacks: %v, changed: %v", res.Fallbacks, res.ChangedSymbols)
			if tc.symbols != nil {
				assert.Equal(t, tc.symbols, res.ChangedSymbols)
			}
		})
	}
}

func TestAnalyze_Origins(t *testing.T) {
	res := analyze(t, map[string]string{"constant/constant.go": replace(baseFiles["constant/constant.go"], "const A = 1", "const A = 10")})
	require.Contains(t, res.Selections, mod+"/usea")
	assert.Equal(t, map[string]string{"TestUseA": mod + "/constant.A"}, res.Selections[mod+"/usea"].Functions)
}

// replace replaces old in s with new, panicking if old is not found.
func replace(s, old, repl string) string {
	if !strings.Contains(s, old) {
		panic("not found: " + old)
	}
	return strings.Replace(s, old, repl, 1)
}
