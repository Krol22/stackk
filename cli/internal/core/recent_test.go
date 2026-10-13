package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/krol22/stackk/cli/internal/store"
)

func TestRecentDirsSkipWorktreesAndGoneFolders(t *testing.T) {
	root := t.TempDir()
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "repo", ".agent-worktrees", "fix")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+repo+"/.git/worktrees/fix\n"), 0o644)
	tasks := []store.Task{
		{Cwd: repo, CreatedAt: 1},
		{Cwd: wt, CreatedAt: 3},
		{Cwd: filepath.Join(root, "gone"), CreatedAt: 4},
		{Cwd: repo, CreatedAt: 2},
	}
	if got := RecentDirs(tasks, 5); len(got) != 1 || got[0] != repo {
		t.Fatalf("only the main checkout, once: %v", got)
	}
}
