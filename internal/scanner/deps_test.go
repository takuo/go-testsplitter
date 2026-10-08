package scanner

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDepModule creates a module:
//
//	lib     no tests
//	app     imports lib
//	tonly   imports lib only in an external test
//	indep   no dependencies
//	fixt    reads testdata/
//	emb     embeds assets/a.txt
func setupDepModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                    "module example.com/m\n\ngo 1.21\n",
		"lib/lib.go":                "package lib\nfunc F() int { return 1 }\n",
		"app/app.go":                "package app\nimport \"example.com/m/lib\"\nfunc G() int { return lib.F() }\n",
		"app/app_test.go":           "package app\nimport \"testing\"\nfunc TestG(t *testing.T) { G() }\n",
		"tonly/tonly.go":            "package tonly\n",
		"tonly/tonly_test.go":       "package tonly_test\nimport (\"testing\"; \"example.com/m/lib\")\nfunc TestX(t *testing.T) { lib.F() }\n",
		"indep/indep_test.go":       "package indep\nimport \"testing\"\nfunc TestI(t *testing.T) {}\n",
		"fixt/fixt_test.go":         "package fixt\nimport \"testing\"\nfunc TestF(t *testing.T) {}\n",
		"fixt/testdata/deep/x.json": "{}\n",
		"emb/emb.go":                "package emb\nimport _ \"embed\"\n//go:embed assets/a.txt\nvar A string\n",
		"emb/assets/a.txt":          "a\n",
		"emb/emb_test.go":           "package emb\nimport \"testing\"\nfunc TestE(t *testing.T) {}\n",
		"docs/readme.md":            "docs\n",
	}
	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}
	return dir
}

func TestDepGraph(t *testing.T) {
	dir := setupDepModule(t)
	t.Chdir(dir)

	g, err := LoadDepGraph([]string{"./..."})
	require.NoError(t, err)

	abs := func(names ...string) []string {
		var out []string
		for _, n := range names {
			out = append(out, filepath.Join(dir, n))
		}
		return out
	}

	changed, unmatched := g.ChangedPackages(abs(
		"lib/lib.go",                // package file
		"fixt/testdata/deep/x.json", // testdata
		"emb/assets/a.txt",          // embedded file
		"docs/readme.md",            // no package
		"lib/removed.go",            // deleted file in an existing package
	))
	assert.Equal(t, []string{"example.com/m/emb", "example.com/m/fixt", "example.com/m/lib"}, changed)
	assert.Equal(t, abs("docs/readme.md"), unmatched)

	assert.Equal(t, []string{"example.com/m/lib"}, g.AffectedBy("example.com/m/app", changed))
	assert.Equal(t, []string{"example.com/m/lib"}, g.AffectedBy("example.com/m/tonly", changed), "test-only import")
	assert.Equal(t, []string{"example.com/m/fixt"}, g.AffectedBy("example.com/m/fixt", changed))
	assert.Equal(t, []string{"example.com/m/emb"}, g.AffectedBy("example.com/m/emb", changed))
	assert.Empty(t, g.AffectedBy("example.com/m/indep", changed))
	assert.Empty(t, g.AffectedBy("example.com/m/unknown", changed))
}

func TestTestdataOwner(t *testing.T) {
	assert.Equal(t, filepath.FromSlash("/r/a"), testdataOwner(filepath.FromSlash("/r/a/testdata/b/testdata/c.txt")))
	assert.Empty(t, testdataOwner(filepath.FromSlash("/r/a/b.go")))
}
