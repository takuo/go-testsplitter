package scanner

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanTestFunctions(t *testing.T) {
	dir := t.TempDir()
	internal := filepath.Join(dir, "example_test.go")
	external := filepath.Join(dir, "example_ext_test.go")
	writeFile(t, internal, `package example

import (
	"testing"
	tt "testing"
)

type Suite struct{}

func TestExample(t *testing.T) {}
func TestAnother(t *tt.T)      {}
func Test(t *testing.T)        {}
func Test_underscore(t *testing.T) {}
func TestMain(m *testing.M)    {}
func Testify(t *testing.T)     {}
func TestNoArgs()              {}
func TestResult(t *testing.T) error { return nil }
func TestGeneric[T any](t *testing.T) {}
func (s *Suite) TestMethod(t *testing.T) {}
func BenchmarkExample(b *testing.B) {}
func helperFunction() {}
`)
	writeFile(t, external, `package example_test

import "testing"

func TestExternal(t *testing.T) {}
`)

	got, err := ScanTestFunctions([]Package{{Dir: "example", TestFiles: []string{internal, external}}})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"example": {"Test", "TestAnother", "TestExample", "TestExternal", "Test_underscore"},
	}, got)
}

func TestScanTestFunctions_NoTests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x_test.go")
	writeFile(t, path, "package x\n")

	got, err := ScanTestFunctions([]Package{{Dir: "x", TestFiles: []string{path}}})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestScanTestFunctions_SyntaxError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x_test.go")
	writeFile(t, path, "package x\nfunc {\n")

	_, err := ScanTestFunctions([]Package{{Dir: "x", TestFiles: []string{path}}})
	assert.Error(t, err)
}
