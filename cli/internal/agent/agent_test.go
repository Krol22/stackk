package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	t.Setenv("STACKK_CODEX_SESSIONS", filepath.Join(dir, "codex"))
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

func TestCodexSessionsAndTranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKK_CODEX_SESSIONS", dir)
	cx := codex{base{"codex"}}
	item := func(it string) string {
		return `{"timestamp":"2026-10-12T12:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":` + it + `}}`
	}
	live := filepath.Join(dir, "2026", "10", "12", "rollout-a.jsonl")
	write(t, live,
		`{"type":"session_meta","payload":{"id":"cx1","cwd":"/w/api","source":"cli"}}`,
		item(`{"type":"UserMessage","content":[{"type":"text","text":"why is the pool exhausted?"}]}`),
		item(`{"type":"CommandExecution","command":["/bin/zsh","-lc","rg pool"],"exit_code":1}`),
		item(`{"type":"AgentMessage","content":[{"type":"Text","text":"The pool is too small."}]}`))
	write(t, filepath.Join(dir, "2026", "10", "12", "rollout-b.jsonl"),
		`{"type":"session_meta","payload":{"id":"cx2","cwd":"/w/api","source":"exec"}}`,
		item(`{"type":"UserMessage","content":[{"type":"text","text":"a one-off exec run"}]}`))
	var got []sessions.Session
	for _, f := range cx.SessionFiles(time.Time{}) {
		if s, ok := cx.ParseSession(f); ok {
			got = append(got, s)
		}
	}
	if len(got) != 1 || got[0].ID != "cx1" || got[0].Title != "why is the pool exhausted?" || got[0].LastText != "The pool is too small." {
		t.Errorf("only the interactive session, titled by its first prompt: %+v", got)
	}
	msgs, err := cx.Messages(live, 10)
	if err != nil || len(msgs) != 3 || msgs[1].Tool != "Bash rg pool" || !msgs[1].Error || msgs[2].Role != "assistant" {
		t.Errorf("transcript: %+v %v", msgs, err)
	}
}

func TestCodexQuestion(t *testing.T) {
	cx := codex{base{"codex"}}
	q, async, ok := cx.Question("request_user_input_async", []byte(`{"questions":[{"title":"Pick a fruit","options":["Apple","Banana"]}]}`))
	if !ok || !async || string(q) != `{"questions":[{"question":"Pick a fruit","options":[{"label":"Apple"},{"label":"Banana"}]}]}` {
		t.Errorf("request_user_input in AskUserQuestion's shape: %s %v %v", q, async, ok)
	}
	if _, _, ok := cx.Question("Bash", nil); ok {
		t.Error("a shell command is no question")
	}
	if a := cx.Answers(2); strings.Join(a.Options[1], " ") != "alt-down wait alt-up wait 2" || strings.Join(a.Own, " ") != "alt-down wait alt-up wait 3" || a.Yes[0] != "y" {
		t.Errorf("answers: %+v", a)
	}
}

func TestCodexHooksAreAddedOnceAndKeepOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"moshi-hook codex-hook"}]}]}}`), 0o600)
	for range 2 {
		if err := installCodexHooks(path, "/bin/stackk", func(string) {}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "stackk mark codex") != len(codexEvents) || !strings.Contains(string(raw), "moshi-hook") {
		t.Errorf("one stackk hook per event, others kept:\n%s", raw)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
}

func TestCodexAnswered(t *testing.T) {
	cx := codex{base{"codex"}}
	prompt := "<send_user_message_question_reply>\n" +
		`[{"answer":"Banana","question":"Pick a fruit","questionItemId":"[\"request_user_input_async\",\"call_1\",0]"}]` +
		"\n</send_user_message_question_reply>"
	keys, answer := cx.Answered(prompt)
	if len(keys) != 1 || keys[0] != "call_1#0" || answer != "Banana" {
		t.Errorf("answered %v %q", keys, answer)
	}
	if keys, _ := cx.Answered("just a prompt [with brackets]"); keys != nil {
		t.Errorf("an ordinary prompt answers nothing: %v", keys)
	}
}
