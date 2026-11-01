package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/arl/gitmux/tmux"
)

// currentDirScope reports working-tree file changes restricted to the current
// working directory. It only computes the counters backing the 'flags' and
// 'stats' components: branch, divergence, stash and operation states
// (merge/rebase/...) stay repository-wide and are read from the regular
// gitstatus query.
//
// The full 'git status --porcelain' listing is fetched once (paths are
// relative to the repository root) and filtered locally, instead of relying on
// a pathspec: when a rename crosses the scope boundary, git splits the rename
// into several pathspec-matching records, which would double-count the change.
func currentDirScope(ctx context.Context) (*tmux.Scope, error) {
	root, prefix, err := gitScopePrefix(ctx)
	if err != nil {
		return nil, err
	}

	out, err := runGit(ctx, root, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	sc := parseScopedPorcelain(out, prefix)

	// Same basis as gitstatus: unstaged ('git diff', not '--cached') line
	// changes. The '.' pathspec limits the diff to the current directory.
	out, err = runGit(ctx, "", "diff", "--shortstat", "--", ".")
	if err != nil {
		return nil, err
	}
	if sc.Insertions, sc.Deletions, err = parseShortStat(out); err != nil {
		return nil, err
	}
	return sc, nil
}

// gitScopePrefix returns the repository root and the path of the current
// directory relative to that root (the "scope prefix"). The prefix is empty
// when the current directory is the repository root, in which case every
// status entry is in scope.
func gitScopePrefix(ctx context.Context) (root, prefix string, err error) {
	out, err := runGit(ctx, "", "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return "", "", err
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", "", fmt.Errorf("can't determine repository root")
	}
	root = lines[0]
	if len(lines) > 1 {
		prefix = strings.TrimSuffix(lines[1], "/")
	}
	return root, prefix, nil
}

// runGit runs git with the given args, optionally in directory dir, and
// returns its standard output.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w (%s)",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// parseScopedPorcelain parses the NUL-separated output of
// 'git status --porcelain=v1 -z' and counts entries matching the scope prefix.
//
// Entry classification mirrors the one performed by github.com/arl/gitstatus,
// so scoped and repository-wide counts use identical rules.
func parseScopedPorcelain(data []byte, prefix string) *tmux.Scope {
	tokens := bytes.Split(data, []byte{0})
	// The listing ends with a trailing NUL, yielding a final empty token.
	if len(tokens) > 0 && len(tokens[len(tokens)-1]) == 0 {
		tokens = tokens[:len(tokens)-1]
	}

	sc := &tmux.Scope{}
	for i := 0; i < len(tokens); i++ {
		rec := tokens[i]
		// An entry is 'XY path', requiring at least the 2 status bytes,
		// one space and a (possibly empty) path.
		if len(rec) < 3 || rec[2] != ' ' {
			continue
		}
		x, y := rec[0], rec[1]
		if x == '#' && y == '#' {
			continue // branch header (not expected with this invocation)
		}

		inScope := pathInScope(prefix, string(rec[3:]))

		// Rename/copy entries carry the original path as an extra
		// NUL-terminated record. The entry belongs to the scope when either
		// side of the rename/copy lies within it, and it is counted once.
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if i+1 < len(tokens) {
				i++
				if !inScope {
					inScope = pathInScope(prefix, string(tokens[i]))
				}
			}
		}
		if !inScope {
			continue
		}

		switch {
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A'):
			sc.NumConflicts++
		case (x == 'A' && y == 'M') || (x == 'M' && y == 'M') ||
			(x == 'M' && y == 'D') || (x == 'R' && y == 'M') ||
			(x == 'R' && y == 'D') || (x == 'A' && y == 'T'):
			sc.NumStaged++
			sc.NumModified++
		case y == 'M' || y == 'D':
			sc.NumModified++
		case x == '?' && y == '?':
			sc.NumUntracked++
		default:
			sc.NumStaged++
		}
	}

	return sc
}

// pathInScope reports whether path (a repository-root-relative path emitted by
// porcelain) lies within the scope prefix. An empty prefix matches everything.
// Matching is path-segment aware so that prefix 'sub' does not match 'submarine'.
func pathInScope(prefix, path string) bool {
	if prefix == "" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// parseShortStat extracts insertion and deletion counts from
// 'git diff --shortstat' output. It mirrors gitstatus.extractShortStat.
func parseShortStat(out []byte) (insertions, deletions int, err error) {
	for _, field := range bytes.Split(out, []byte{','}) {
		field := bytes.TrimSpace(field)
		switch {
		case bytes.Contains(field, []byte("insertion")):
			pos := bytes.Index(field, []byte("insertion"))
			if insertions, err = strconv.Atoi(string(bytes.TrimSpace(field[:pos]))); err != nil {
				return 0, 0, err
			}
		case bytes.Contains(field, []byte("deletion")):
			pos := bytes.Index(field, []byte("deletion"))
			if deletions, err = strconv.Atoi(string(bytes.TrimSpace(field[:pos]))); err != nil {
				return 0, 0, err
			}
		}
	}
	return insertions, deletions, nil
}
