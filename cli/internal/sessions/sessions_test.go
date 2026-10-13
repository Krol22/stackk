package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAIOrderAndBadReply(t *testing.T) {
	all := []Session{{ID: "a", Title: "alpha", Path: "a"}, {ID: "b", Title: "beta", Path: "b"}}
	t.Setenv("STACKK_AI_CMD", `echo 'Sure: [2, 1]'`)
	hits, err := AI(all, "something", 5)
	if err != nil || len(hits) != 2 || hits[0].ID != "b" || !hits[0].AI {
		t.Fatalf("ai: %+v %v", hits, err)
	}
	t.Setenv("STACKK_AI_CMD", `echo 'no idea'`)
	if _, err := AI(all, "x", 5); err == nil {
		t.Fatal("a reply without an array must be an error")
	}
}

func TestAIWorkDirIsPrivateAndEmpty(t *testing.T) {
	t.Setenv("STACKK_DB", filepath.Join(t.TempDir(), "state", "stackk.db"))
	dir, err := aiWorkDir()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o755) // planted settings
	if dir, err = aiWorkDir(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Error("planted .claude survived")
	}
}
