package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestChangedSince(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init")
	write(t, dir, "a/a.go", "package a\n")
	write(t, dir, "b/b.go", "package b\n")
	write(t, dir, "c/old.go", "package c\n")
	write(t, dir, "keep.txt", "keep\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "init")

	// main moves forward after the branch point; its changes must not be included
	run(t, dir, "switch", "-c", "feature")
	run(t, dir, "switch", "main")
	write(t, dir, "keep.txt", "changed on main\n")
	run(t, dir, "commit", "-am", "main change")
	run(t, dir, "switch", "feature")

	write(t, dir, "a/a.go", "package a // committed\n")
	run(t, dir, "mv", "c/old.go", "c/new.go")
	run(t, dir, "commit", "-am", "feature change")
	write(t, dir, "b/b.go", "package b // uncommitted\n")
	write(t, dir, "d/untracked file.go", "package d\n")
	write(t, dir, ".gitignore", "ignored/\n")
	write(t, dir, "ignored/x.go", "package ignored\n")

	// from a subdirectory
	got, err := ChangedSince(context.Background(), filepath.Join(dir, "a"), "main")
	require.NoError(t, err)

	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	gotRoot, err := filepath.EvalSymlinks(got.Root)
	require.NoError(t, err)
	assert.Equal(t, root, gotRoot)
	assert.Len(t, got.Base, 40)
	assert.Equal(t, []string{".gitignore", "a/a.go", "b/b.go", "c/new.go", "c/old.go", "d/untracked file.go"}, got.Files)
}

func TestChangedSince_UnknownRevision(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init")
	write(t, dir, "a.txt", "a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "init")

	_, err := ChangedSince(context.Background(), dir, "no-such-branch")
	assert.ErrorContains(t, err, "merge base of no-such-branch")
}

func TestChangedSince_NotRepository(t *testing.T) {
	_, err := ChangedSince(context.Background(), t.TempDir(), "main")
	assert.Error(t, err)
}
