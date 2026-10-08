// Package gitdiff lists files changed in a git repository.
package gitdiff

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Changes is the files changed since a base revision.
type Changes struct {
	// Root is the top-level directory of the repository.
	Root string
	// Base is the merge base commit of the base revision and HEAD, or the base revision itself
	// if Direct is true.
	Base string
	// Direct reports whether the merge base could not be found in a shallow clone, and the changes are
	// compared with the base revision directly. Then changes made on the base revision side after the
	// branch point are also included.
	Direct bool
	// Files is the changed files relative to Root, slash-separated and sorted.
	// It includes committed and uncommitted changes since Base, deleted files, and untracked files.
	// Renamed files are listed with both the old and new paths.
	Files []string
}

// ChangedSince returns files changed since the merge base of rev and HEAD, in the repository containing dir.
//
// In a shallow clone, the merge base may not be found since the history is truncated.
// Then the changes are compared with rev directly (Changes.Direct). To get exact changes in a shallow
// clone, fetch the merge base commit (e.g. from the GitHub API) and pass it as rev.
func ChangedSince(ctx context.Context, dir, rev string) (*Changes, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root = strings.TrimSpace(root)

	commit, err := git(ctx, root, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("unknown revision %s: %w", rev, err)
	}
	commit = strings.TrimSpace(commit)

	direct := false
	base, err := git(ctx, root, "merge-base", commit, "HEAD")
	if err != nil {
		shallow, serr := git(ctx, root, "rev-parse", "--is-shallow-repository")
		if serr != nil || strings.TrimSpace(shallow) != "true" {
			return nil, fmt.Errorf("failed to find the merge base of %s and HEAD: %w", rev, err)
		}
		base, direct = commit, true
	}
	base = strings.TrimSpace(base)

	// Compare the working tree with the merge base, so that uncommitted changes are included.
	diff, err := git(ctx, root, "diff", "--name-only", "--no-renames", "-z", base, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}

	var files []string
	for _, out := range []string{diff, untracked} {
		for f := range strings.SplitSeq(out, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	slices.Sort(files)
	return &Changes{Root: root, Base: base, Direct: direct, Files: slices.Compact(files)}, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// Show returns the content of path (relative to the repository root, slash-separated) at rev.
// It returns false if the file does not exist at rev.
func Show(ctx context.Context, root, rev, path string) ([]byte, bool, error) {
	if _, err := git(ctx, root, "cat-file", "-e", rev+":"+path); err != nil {
		return nil, false, nil
	}
	out, err := git(ctx, root, "cat-file", "blob", rev+":"+path)
	if err != nil {
		return nil, false, err
	}
	return []byte(out), true, nil
}
