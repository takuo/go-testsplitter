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
	// Base is the merge base commit of the base revision and HEAD.
	Base string
	// Files is the changed files relative to Root, slash-separated and sorted.
	// It includes committed and uncommitted changes since Base, deleted files, and untracked files.
	// Renamed files are listed with both the old and new paths.
	Files []string
}

// ChangedSince returns files changed since the merge base of rev and HEAD, in the repository containing dir.
func ChangedSince(ctx context.Context, dir, rev string) (*Changes, error) {
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root = strings.TrimSpace(root)
	base, err := git(ctx, root, "merge-base", rev, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to find the merge base of %s and HEAD: %w", rev, err)
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
	return &Changes{Root: root, Base: base, Files: slices.Compact(files)}, nil
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
