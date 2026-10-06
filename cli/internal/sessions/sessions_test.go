package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path string, lines ...string) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseClaudeAndOmp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKK_CLAUDE_PROJECTS", filepath.Join(dir, "claude"))
	t.Setenv("STACKK_OMP_SESSIONS", filepath.Join(dir, "omp"))
	write(t, filepath.Join(dir, "claude", "-w-shop", "c1.jsonl"),
		`{"type":"user","sessionId":"c1","cwd":"/w/shop","message":{"content":"return search on Mondial Relay fails"}}`,
		`{"type":"user","sessionId":"c1","cwd":"/w/shop","message":{"content":[{"type":"tool_result","content":"x"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Fixed the STAT 24 error."}]}}`,
		`{"type":"ai-title","aiTitle":"Mondial Relay return search"}`,
		`{"type":"user","isSidechain":true,"message":{"content":"subagent prompt"}}`)
	write(t, filepath.Join(dir, "omp", "-w-wiki", "o1.jsonl"),
		`{"type":"title","title":"Wiki migration review"}`,
		`{"type":"session","id":"o1","cwd":"/w/wiki","title":"old title"}`,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"what did we change in the wiki?"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Moved 12 pages."}]}}`)
	files := Files(24*365*1e9*3600, 10)
	if len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
	var all []Session
	for _, f := range files {
		s, ok := Parse(f)
		if !ok {
			t.Fatalf("parse %s", f.Path)
		}
		all = append(all, s)
	}
	byID := map[string]Session{}
	for _, s := range all {
		byID[s.ID] = s
	}
	c := byID["c1"]
	if c.Title != "Mondial Relay return search" || c.Cwd != "/w/shop" || len(c.Prompts) != 1 || c.LastText != "Fixed the STAT 24 error." {
		t.Errorf("claude session: %+v", c)
	}
	o := byID["o1"]
	if o.Title != "Wiki migration review" || o.Cwd != "/w/wiki" || o.Prompts[0] != "what did we change in the wiki?" {
		t.Errorf("omp session: %+v", o)
	}
	if hits := Local(all, "mondial", 10); len(hits) != 1 || hits[0].ID != "c1" {
		t.Errorf("local search: %+v", hits)
	}
	if hits := Local(all, "", 10); len(hits) != 2 {
		t.Errorf("empty query should list recent: %d", len(hits))
	}
}

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
