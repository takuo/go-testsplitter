package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	assert.ErrorContains(t, err, "unknown revision no-such-branch")
}

func TestChangedSince_NotRepository(t *testing.T) {
	_, err := ChangedSince(context.Background(), t.TempDir(), "main")
	assert.Error(t, err)
}

func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}

func TestChangedSince_ShallowClone(t *testing.T) {
	// origin: main moves forward after feature branches off
	origin := t.TempDir()
	run(t, origin, "init")
	run(t, origin, "config", "uploadpack.allowReachableSHA1InWant", "true")
	write(t, origin, "a.txt", "a\n")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-m", "base")
	run(t, origin, "switch", "-c", "feature")
	write(t, origin, "f.txt", "f\n")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-m", "feature 1")
	write(t, origin, "g.txt", "g\n")
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-m", "feature 2")
	run(t, origin, "switch", "main")
	write(t, origin, "a.txt", "changed on main\n")
	run(t, origin, "commit", "-am", "main change")
	mergeBase := output(t, origin, "merge-base", "main", "feature")

	work := filepath.Join(t.TempDir(), "work")
	run(t, filepath.Dir(work), "clone", "--depth=1", "--branch", "feature", "file://"+origin, work)
	ctx := context.Background()

	t.Run("merge base commit fetched by SHA", func(t *testing.T) {
		run(t, work, "fetch", "--depth=1", "origin", mergeBase)
		got, err := ChangedSince(ctx, work, mergeBase)
		require.NoError(t, err)
		assert.True(t, got.Direct)
		assert.Equal(t, mergeBase, got.Base)
		assert.Equal(t, []string{"f.txt", "g.txt"}, got.Files, "exactly the changes of the branch")
	})

	t.Run("base branch tip", func(t *testing.T) {
		run(t, work, "fetch", "--depth=1", "origin", "+refs/heads/main:refs/remotes/origin/main")
		got, err := ChangedSince(ctx, work, "origin/main")
		require.NoError(t, err)
		assert.True(t, got.Direct)
		assert.Equal(t, []string{"a.txt", "f.txt", "g.txt"}, got.Files, "includes changes on main")
	})
}

func TestChangedSince_UnrelatedHistories(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init")
	write(t, dir, "a.txt", "a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "init")
	run(t, dir, "switch", "--orphan", "other")
	write(t, dir, "b.txt", "b\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "other")

	_, err := ChangedSince(context.Background(), dir, "main")
	assert.ErrorContains(t, err, "merge base of main")
}
