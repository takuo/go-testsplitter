package command

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resultLine = `{"Time":"2026-10-01T00:00:02Z","Action":"pass","Package":"p","Test":"TestA","Elapsed":1}`
	outputLine = `{"Time":"2026-10-01T00:00:01Z","Action":"output","Package":"p","Test":"TestA","Output":"=== RUN\n"}`
)

// archiveDir returns a JSON directory with the results of two nodes.
func archiveDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"test-0.jsonl", "test-1.jsonl"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(outputLine+"\n"+resultLine+"\n"), 0o644))
	}
	return dir
}

func fixedTime(t *testing.T) func() time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339, "2026-10-22T09:08:07Z")
	require.NoError(t, err)
	return func() time.Time { return now }
}

func TestArchiveValidate(t *testing.T) {
	require.NoError(t, (&ArchiveCmd{Keep: 1}).Validate())
	assert.ErrorContains(t, (&ArchiveCmd{Keep: -1}).Validate(), "--keep")
	assert.ErrorContains(t, (&ArchiveCmd{MaxAge: -time.Second}).Validate(), "--max-age")
}

func TestArchiveRun(t *testing.T) {
	dir := archiveDir(t)
	cmd := &ArchiveCmd{JSONDir: dir, Keep: 20, now: fixedTime(t)}
	require.NoError(t, cmd.Run())

	runDir := filepath.Join(dir, "runs", "20261022T090807Z")
	for _, name := range []string{"test-0.jsonl", "test-1.jsonl"} {
		b, err := os.ReadFile(filepath.Join(runDir, name))
		require.NoError(t, err)
		assert.Equal(t, resultLine+"\n", string(b), "only the results needed for splitting are archived")
		assert.NoFileExists(t, filepath.Join(dir, name), "archived files are moved")
	}

	// A second run is archived next to the first one
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-0.jsonl"), []byte(resultLine+"\n"), 0o644))
	require.NoError(t, cmd.Run())
	entries, err := os.ReadDir(filepath.Join(dir, "runs"))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"20261022T090807Z", "20261022T090807Z-2"},
		[]string{entries[0].Name(), entries[1].Name()})
}

func TestArchiveRun_Options(t *testing.T) {
	t.Run("keep-source and keep-output", func(t *testing.T) {
		dir := archiveDir(t)
		cmd := &ArchiveCmd{JSONDir: dir, ID: "run-1", KeepSource: true, KeepOutput: true}
		require.NoError(t, cmd.Run())

		b, err := os.ReadFile(filepath.Join(dir, "runs", "run-1", "test-0.jsonl"))
		require.NoError(t, err)
		assert.Equal(t, outputLine+"\n"+resultLine+"\n", string(b))
		assert.FileExists(t, filepath.Join(dir, "test-0.jsonl"))
	})

	t.Run("dry-run", func(t *testing.T) {
		dir := archiveDir(t)
		cmd := &ArchiveCmd{JSONDir: dir, DryRun: true, now: fixedTime(t)}
		require.NoError(t, cmd.Run())

		assert.NoDirExists(t, filepath.Join(dir, "runs"))
		assert.FileExists(t, filepath.Join(dir, "test-0.jsonl"))
	})

	t.Run("explicit files and runs directory", func(t *testing.T) {
		dir := archiveDir(t)
		runs := filepath.Join(t.TempDir(), "history")
		cmd := &ArchiveCmd{JSONDir: dir, RunsDir: runs, ID: "run-1"}
		require.NoError(t, cmd.Run())
		assert.FileExists(t, filepath.Join(runs, "run-1", "test-1.jsonl"))

		// Archiving into the same run keeps both files
		other := filepath.Join(t.TempDir(), "test-1.jsonl")
		require.NoError(t, os.WriteFile(other, []byte(resultLine+"\n"), 0o644))
		cmd.Files = []string{other}
		require.NoError(t, cmd.Run())
		assert.FileExists(t, filepath.Join(runs, "run-1", "test-1-2.jsonl"))
	})

	t.Run("missing file", func(t *testing.T) {
		cmd := &ArchiveCmd{JSONDir: t.TempDir(), Files: []string{"missing.jsonl"}}
		assert.ErrorContains(t, cmd.Run(), "missing.jsonl")
	})

	t.Run("no results", func(t *testing.T) {
		dir := t.TempDir()
		cmd := &ArchiveCmd{JSONDir: dir}
		require.NoError(t, cmd.Run(), "archiving nothing is not an error")
		assert.NoDirExists(t, filepath.Join(dir, "runs"))
	})

	t.Run("already archived files are skipped", func(t *testing.T) {
		dir := archiveDir(t)
		cmd := &ArchiveCmd{JSONDir: dir, RunsDir: dir, ID: "run-1"}
		require.NoError(t, cmd.Run())
		require.NoError(t, cmd.Run(), "the archived run is not archived again")

		entries, err := os.ReadDir(filepath.Join(dir, "run-1"))
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})
}

func TestArchivePrune(t *testing.T) {
	now := fixedTime(t)()
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	for i, age := range []time.Duration{0, 24 * time.Hour, 48 * time.Hour, 72 * time.Hour} {
		run := filepath.Join(runs, "run-"+string(rune('a'+i)))
		require.NoError(t, os.MkdirAll(run, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(run, "test-0.jsonl"), []byte(resultLine+"\n"), 0o644))
		require.NoError(t, os.Chtimes(run, now.Add(-age), now.Add(-age)))
	}
	require.NoError(t, os.WriteFile(filepath.Join(runs, "stray.txt"), nil, 0o644))

	cmd := &ArchiveCmd{JSONDir: dir, Keep: 2, now: func() time.Time { return now }}
	require.NoError(t, cmd.Run())
	assertRuns := func(want ...string) {
		t.Helper()
		var got []string
		entries, err := os.ReadDir(runs)
		require.NoError(t, err)
		for _, e := range entries {
			if e.IsDir() {
				got = append(got, e.Name())
			}
		}
		assert.Equal(t, want, got)
	}
	assertRuns("run-a", "run-b")
	assert.FileExists(t, filepath.Join(runs, "stray.txt"), "files in the runs directory are left alone")

	cmd = &ArchiveCmd{JSONDir: dir, MaxAge: 12 * time.Hour, now: func() time.Time { return now }}
	require.NoError(t, cmd.Run())
	assertRuns("run-a")
}
