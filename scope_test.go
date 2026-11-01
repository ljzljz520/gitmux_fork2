package main

import (
	"testing"

	"github.com/arl/gitmux/tmux"
)

// rec builds a NUL-terminated porcelain v1 record (with an optional original
// path, as emitted for renames/copies under -z).
func rec(xy, path string, orig ...string) []byte {
	b := []byte(xy + " " + path + "\x00")
	for _, p := range orig {
		b = append(b, []byte(p+"\x00")...)
	}
	return b
}

func TestPathInScope(t *testing.T) {
	tests := []struct {
		prefix string
		path   string
		want   bool
	}{
		{"", "anything/goes", true},      // repository root: everything is in scope
		{"sub", "sub", true},             // the scope directory itself
		{"sub", "sub/file", true},        // nested file
		{"sub", "sub/deep/file", true},   // nested directory
		{"sub", "sub/", true},            // untracked directory entry
		{"sub", "submarine", false},      // path-segment aware: same prefix, different segment
		{"sub", "submarine/file", false}, // sibling directory
		{"sub", "other/file", false},     // outside
		{"a/b", "a/b/c", true},           // nested prefix
		{"a/b", "a/b-c", false},          // segment boundary
		{"a/b", "a/b", true},             // exact match
		{"a/b", "a/c", false},            // sibling
	}
	for _, tt := range tests {
		if got := pathInScope(tt.prefix, tt.path); got != tt.want {
			t.Errorf("pathInScope(%q, %q) = %v, want %v", tt.prefix, tt.path, got, tt.want)
		}
	}
}

func TestParseScopedPorcelain(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		records [][]byte
		want    tmux.Scope
	}{
		{
			name:   "empty listing",
			prefix: "sub",
			want:   tmux.Scope{},
		},
		{
			name:   "changes outside the scope are ignored",
			prefix: "sub",
			records: [][]byte{
				rec("##", "main...origin/main"),
				rec(" M", "other/modified.txt"),
				rec("A ", "other/added.txt"),
				rec("??", "other/untracked.txt"),
				rec("UU", "other/conflict.txt"),
			},
			want: tmux.Scope{},
		},
		{
			name:   "mixed changes inside the scope",
			prefix: "sub",
			records: [][]byte{
				rec(" M", "sub/modified.txt"),
				rec(" M", "other/modified.txt"),
				rec("A ", "sub/added.txt"),
				rec("??", "sub/untracked.txt"),
				rec("??", "other/untracked.txt"),
				rec("UU", "sub/conflict.txt"),
				rec("D ", "sub/deleted.txt"),
			},
			want: tmux.Scope{
				NumStaged:    2, // added + deleted
				NumConflicts: 1,
				NumModified:  1,
				NumUntracked: 1,
			},
		},
		{
			name:   "staged and worktree modified",
			prefix: "sub",
			records: [][]byte{
				rec("MM", "sub/both.txt"),
				rec("AM", "sub/added-then-modified.txt"),
				rec("MD", "sub/staged-then-deleted.txt"),
				rec("AT", "sub/typechange.txt"),
			},
			want: tmux.Scope{NumStaged: 4, NumModified: 4},
		},
		{
			name:   "rename from outside into the scope is counted once",
			prefix: "sub",
			records: [][]byte{
				rec("R ", "sub/inside.txt", "outside.txt"),
			},
			want: tmux.Scope{NumStaged: 1},
		},
		{
			name:   "rename from the scope to outside is counted once",
			prefix: "sub",
			records: [][]byte{
				rec("R ", "outside.txt", "sub/inside.txt"),
			},
			want: tmux.Scope{NumStaged: 1},
		},
		{
			name:   "rename fully outside the scope is ignored",
			prefix: "sub",
			records: [][]byte{
				rec("R ", "d/x.txt", "c/y.txt"),
			},
			want: tmux.Scope{},
		},
		{
			name:   "rename within the scope is counted once",
			prefix: "sub",
			records: [][]byte{
				rec("R ", "sub/new.txt", "sub/old.txt"),
			},
			want: tmux.Scope{NumStaged: 1},
		},
		{
			name:   "rename crossing into the scope with worktree modifications",
			prefix: "sub",
			records: [][]byte{
				rec("RM", "sub/new.txt", "outside.txt"),
				rec("RD", "sub/new2.txt", "sub/old2.txt"),
			},
			want: tmux.Scope{NumStaged: 2, NumModified: 2},
		},
		{
			name:   "copy crossing the scope boundary",
			prefix: "sub",
			records: [][]byte{
				rec("C ", "sub/copy.txt", "other/original.txt"),
			},
			want: tmux.Scope{NumStaged: 1},
		},
		{
			name:   "path prefix matches by segment, not by string",
			prefix: "sub",
			records: [][]byte{
				rec("??", "submarine"),
				rec(" M", "submarine/f.txt"),
				rec("??", "sub"),
				rec(" M", "sub/in.txt"),
			},
			want: tmux.Scope{NumModified: 1, NumUntracked: 1},
		},
		{
			name:   "special characters in paths are handled (-z, raw paths)",
			prefix: "sub",
			records: [][]byte{
				rec(" M", "sub/a b.txt"),
				rec(" M", "sub/weird [dir]/f*.txt"),
				rec(" M", "sub/-leading-dash.txt"),
				rec(" M", "sub/中文 文件.txt"),
				rec(" M", "sub/tab\tname.txt"),
				rec("??", "sub/new dir/"),
			},
			want: tmux.Scope{NumModified: 5, NumUntracked: 1},
		},
		{
			name:   "conflict flavours within the scope",
			prefix: "sub",
			records: [][]byte{
				rec("DU", "sub/a"),
				rec("UD", "sub/b"),
				rec("AA", "sub/c"),
				rec("AU", "sub/e"),
				// 'DD' is classified like gitstatus does: worktree-side
				// deletion, hence a modified file.
				rec("DD", "sub/d"),
			},
			want: tmux.Scope{NumConflicts: 4, NumModified: 1},
		},
		{
			name:   "nested scope prefix",
			prefix: "a/b",
			records: [][]byte{
				rec(" M", "a/b/in.txt"),
				rec(" M", "a/c/out.txt"),
				rec("??", "a/b-c/out.txt"),
			},
			want: tmux.Scope{NumModified: 1},
		},
		{
			name:   "empty prefix scopes to the whole repository",
			prefix: "",
			records: [][]byte{
				rec(" M", "anywhere/f.txt"),
				rec("??", "deep/deep/g.txt"),
				rec("R ", "x.txt", "y.txt"),
			},
			want: tmux.Scope{NumStaged: 1, NumModified: 1, NumUntracked: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data []byte
			for _, r := range tt.records {
				data = append(data, r...)
			}
			got := parseScopedPorcelain(data, tt.prefix)
			if *got != tt.want {
				t.Errorf("parseScopedPorcelain:\n got=%#v\nwant=%#v", *got, tt.want)
			}
		})
	}
}

func TestParseShortStat(t *testing.T) {
	tests := []struct {
		out      string
		ins, del int
		wantErr  bool
	}{
		{"", 0, 0, false},
		{"\n", 0, 0, false},
		{" 1 file changed, 12 insertions(+)\n", 12, 0, false},
		{" 2 files changed, 3 deletions(-)\n", 0, 3, false},
		{" 3 files changed, 56 insertions(+), 21 deletions(-)\n", 56, 21, false},
		{" 1 file changed, 0 insertions(+), 0 deletions(-)\n", 0, 0, false},
		{"nothing to report", 0, 0, false},
		{" x insertions(+)\n", 0, 0, true},
	}
	for _, tt := range tests {
		ins, del, err := parseShortStat([]byte(tt.out))
		if (err != nil) != tt.wantErr {
			t.Errorf("parseShortStat(%q): err=%v, wantErr=%v", tt.out, err, tt.wantErr)
		}
		if ins != tt.ins || del != tt.del {
			t.Errorf("parseShortStat(%q) = (%d,%d), want (%d,%d)", tt.out, ins, del, tt.ins, tt.del)
		}
	}
}
