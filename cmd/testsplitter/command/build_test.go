package command

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/takuo/go-testsplitter/internal/scanner"
)

func TestGoTestExecName(t *testing.T) {
	cases := map[string]string{
		"example.com/m/api/foo": "foo.test",
		"example.com/m/c.d":     "c.d.test",
		"example.com/m/v2":      "m.test",
		"example.com/m/v2/foo":  "foo.test",
		"example.com/m/v1":      "v1.test",
		"example.com/m/v02":     "v02.test",
		"example.com/m/vx":      "vx.test",
		"v2":                    "v2.test",
		"foo":                   "foo.test",
	}
	for in, want := range cases {
		assert.Equal(t, want, goTestExecName(in), in)
	}
}

func TestBuildBatches(t *testing.T) {
	pkgs := []scanner.Package{
		{ImportPath: "m/a/x"},
		{ImportPath: "m/b/x"},
		{ImportPath: "m/y"},
		{ImportPath: "m/c/x"},
		{ImportPath: "m/d/y"},
	}
	assert.Equal(t, [][]scanner.Package{
		{{ImportPath: "m/a/x"}, {ImportPath: "m/y"}},
		{{ImportPath: "m/b/x"}, {ImportPath: "m/d/y"}},
		{{ImportPath: "m/c/x"}},
	}, buildBatches(pkgs))
}

func TestBinaryName(t *testing.T) {
	assert.Equal(t, "api.service.foo.test", binaryName("api/service/foo"))
}
