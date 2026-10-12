package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krol22/stackk/cli/internal/sessions"
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
	var all []sessions.Session
	for _, f := range files {
		s, ok := Parse(f)
		if !ok {
			t.Fatalf("parse %s", f.Path)
		}
		all = append(all, s)
	}
	byID := map[string]sessions.Session{}
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
	if hits := sessions.Local(all, "mondial", 10); len(hits) != 1 || hits[0].ID != "c1" {
		t.Errorf("local search: %+v", hits)
	}
	if hits := sessions.Local(all, "", 10); len(hits) != 2 {
		t.Errorf("empty query should list recent: %d", len(hits))
	}
}

func TestPromptIsNeverAnAgentOption(t *testing.T) {
	argv, err := base{"sh"}.Argv("--dangerously-skip-permissions", "")
	if err != nil {
		t.Fatal(err)
	}
	if last := argv[len(argv)-1]; last[0] == '-' {
		t.Errorf("the prompt reached the agent as an option: %q", argv)
	}
}

func TestAnswers(t *testing.T) {
	c, o := claude{base{"claude"}}.Answers(3), omp{base{"omp"}}.Answers(3)
	if strings.Join(c.Options[1], " ") != "2" || strings.Join(c.Own, " ") != "4" {
		t.Errorf("claude: option n is the digit n, own answer the next: %+v", c)
	}
	if strings.Join(o.Options[1], " ") != "up up up up down enter" || strings.Join(o.Own, " ") != "up up up up down down down enter" {
		t.Errorf("omp: to the top, then down: %+v", o)
	}
	if a := (base{"x"}).Answers(3); a.Options != nil || a.Yes != nil {
		t.Errorf("an agent that says nothing offers no answers: %+v", a)
	}
}
