package command

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/takuo/go-testsplitter/internal/archive"
)

// defaultResultGlob matches the result files written by the generated scripts.
const defaultResultGlob = "test-*.jsonl"

// ArchiveCmd archives the results of a test run under the previous results, so that the next
// split can use them. Results are compacted, kept per run, and old runs are removed.
type ArchiveCmd struct {
	Files []string `arg:"" optional:"" placeholder:"FILE" help:"Result files to archive (default: JSON_DIR/test-*.jsonl)"`

	JSONDir    string        `short:"j" long:"json-dir" default:"./test-json" help:"Directory containing the results of this run and the archived runs"`
	RunsDir    string        `long:"runs-dir" placeholder:"DIR" help:"Directory to archive runs into (default: JSON_DIR/runs)"`
	ID         string        `long:"id" placeholder:"NAME" help:"Name of the directory of this run, e.g. the CI workflow ID (default: the current UTC time)"`
	Keep       int           `long:"keep" default:"20" help:"Number of archived runs to keep, newest first (0: keep all)"`
	MaxAge     time.Duration `long:"max-age" default:"0s" help:"Remove archived runs older than this duration, e.g. 720h (0: no limit)"`
	KeepOutput bool          `long:"keep-output" help:"Archive all events including test output (default: only the results needed for splitting)"`
	KeepSource bool          `long:"keep-source" help:"Keep the archived files in JSON_DIR instead of moving them"`

	DryRun bool `long:"dry-run" help:"Print what would be archived and removed without writing anything"`

	Quiet   bool `short:"q" long:"quiet" xor:"verbosity" help:"Print only warnings and errors"`
	Verbose bool `long:"verbose" xor:"verbosity" help:"Print debug logs"`

	// Runtime context
	now func() time.Time `kong:"-"`
}

// Validate validates the command line arguments (called by kong).
func (a *ArchiveCmd) Validate() error {
	var errs []error
	if a.Keep < 0 {
		errs = append(errs, errors.New("--keep must be >= 0"))
	}
	if a.MaxAge < 0 {
		errs = append(errs, errors.New("--max-age must be >= 0"))
	}
	return errors.Join(errs...)
}

// Run archives the results of this run and removes old archived runs.
func (a *ArchiveCmd) Run() error {
	slog.SetDefault(newLogger(os.Stderr, a.logLevel()))

	runsDir, err := filepath.Abs(a.runsDir())
	if err != nil {
		return err
	}
	files, err := a.sources(runsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		slog.Warn("No test results to archive", "dir", a.JSONDir, "pattern", defaultResultGlob)
	} else if err := a.archiveFiles(runsDir, files); err != nil {
		return err
	}
	return a.pruneRuns(runsDir)
}

func (a *ArchiveCmd) runsDir() string {
	if a.RunsDir != "" {
		return a.RunsDir
	}
	return filepath.Join(a.JSONDir, "runs")
}

func (a *ArchiveCmd) timeNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// sources returns the result files to archive: the given files, or JSON_DIR/test-*.jsonl.
// Files of archived runs are skipped, so that they are not archived again.
func (a *ArchiveCmd) sources(runsDir string) ([]string, error) {
	files := a.Files
	if len(files) == 0 {
		matches, err := filepath.Glob(filepath.Join(a.JSONDir, defaultResultGlob))
		if err != nil {
			return nil, err
		}
		files = matches
	}

	var out []string
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		if archived(runsDir, abs) {
			slog.Debug("Skipping already archived file", "file", f)
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("cannot archive %s: %w", f, err)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("cannot archive %s: is a directory", f)
		}
		out = append(out, abs)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// archived reports whether path is in a run directory of runsDir, and hence already archived.
// Files directly in runsDir are not, so that --runs-dir can be the JSON directory itself.
func archived(runsDir, path string) bool {
	rel, err := filepath.Rel(runsDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return strings.Contains(rel, string(filepath.Separator))
}

func (a *ArchiveCmd) archiveFiles(runsDir string, files []string) error {
	runDir, err := a.makeRunDir(runsDir)
	if err != nil {
		return err
	}
	var total archive.Stats
	for _, src := range files {
		dst, err := uniquePath(runDir, filepath.Base(src), a.DryRun)
		if err != nil {
			return err
		}
		st, err := a.archiveFile(src, dst)
		if err != nil {
			return err
		}
		total.Kept += st.Kept
		total.Dropped += st.Dropped
		total.BytesIn += st.BytesIn
		total.BytesOut += st.BytesOut
		slog.Debug("Archived test results", "src", src, "dst", dst,
			"events", st.Kept, "dropped", st.Dropped, "bytes_in", st.BytesIn, "bytes_out", st.BytesOut)
	}
	slog.Info("Archived test results", "run", filepath.Base(runDir), "files", len(files),
		"events", total.Kept, "dropped", total.Dropped, "bytes_in", total.BytesIn, "bytes_out", total.BytesOut,
		"dry_run", a.DryRun)
	return nil
}

// archiveFile writes the compacted results of src to dst, and removes src unless --keep-source.
func (a *ArchiveCmd) archiveFile(src, dst string) (st archive.Stats, err error) {
	in, err := os.Open(src)
	if err != nil {
		return st, err
	}
	defer in.Close() //nolint:errcheck // read only

	out := io.Discard
	if !a.DryRun {
		f, cerr := os.Create(dst) // not err, so that the deferred close reports on the named return
		if cerr != nil {
			return st, cerr
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()
		out = f
	}

	if a.KeepOutput {
		st, err = archive.CopyAll(out, in)
	} else {
		st, err = archive.Compact(out, in)
	}
	if err != nil {
		return st, fmt.Errorf("failed to archive %s: %w", src, err)
	}
	if !a.KeepSource && !a.DryRun {
		if err := os.Remove(src); err != nil {
			return st, err
		}
	}
	return st, nil
}

// makeRunDir creates the directory of this run. The default name is the current UTC time,
// made unique if it already exists. A name given with --id is used as it is, so that
// several invocations can archive into the same run.
func (a *ArchiveCmd) makeRunDir(runsDir string) (string, error) {
	if a.ID != "" {
		dir := filepath.Join(runsDir, a.ID)
		if a.DryRun {
			return dir, nil
		}
		return dir, os.MkdirAll(dir, 0o755)
	}
	name := a.timeNow().UTC().Format("20060102T150405Z")
	dir, err := uniquePath(runsDir, name, a.DryRun)
	if err != nil || a.DryRun {
		return dir, err
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// uniquePath returns dir/name, with a numeric suffix if it already exists.
func uniquePath(dir, name string, dryRun bool) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		path := filepath.Join(dir, name)
		if i > 1 {
			path = filepath.Join(dir, base+"-"+strconv.Itoa(i)+ext)
		}
		_, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return path, nil
		case err != nil:
			return "", err
		case dryRun:
			return path, nil // nothing is written, so the existing path is reported as is
		case i == 100:
			return "", fmt.Errorf("too many files named %s in %s", name, dir)
		}
	}
}

// pruneRuns removes archived runs beyond --keep and older than --max-age, by directory time.
func (a *ArchiveCmd) pruneRuns(runsDir string) error {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	type run struct {
		path string
		mod  time.Time
	}
	var runs []run
	for _, e := range entries {
		if !e.IsDir() {
			slog.Debug("Ignoring non-directory in the runs directory", "name", e.Name())
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		runs = append(runs, run{filepath.Join(runsDir, e.Name()), fi.ModTime()})
	}
	// Newest first, so that the newest runs are kept
	slices.SortFunc(runs, func(x, y run) int {
		return cmp.Or(y.mod.Compare(x.mod), strings.Compare(y.path, x.path))
	})

	var cutoff time.Time
	if a.MaxAge > 0 {
		cutoff = a.timeNow().Add(-a.MaxAge)
	}
	removed := 0
	for i, r := range runs {
		var reason string
		switch {
		case a.Keep > 0 && i >= a.Keep:
			reason = "--keep"
		case !cutoff.IsZero() && r.mod.Before(cutoff):
			reason = "--max-age"
		default:
			continue
		}
		slog.Debug("Removing archived run", "run", filepath.Base(r.path), "reason", reason, "modified", r.mod)
		if !a.DryRun {
			if err := os.RemoveAll(r.path); err != nil {
				return err
			}
		}
		removed++
	}
	slog.Info("Pruned archived runs", "removed", removed, "kept", len(runs)-removed, "dir", runsDir, "dry_run", a.DryRun)
	return nil
}
