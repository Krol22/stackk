package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeHooksAreAddedOnceAndKeepOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-tool"}]}]}}`), 0o600)
	var log []string
	out := func(s string) { log = append(log, s) }
	if err := installClaudeHooks(path, "/bin/stackk", out); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooks(path, "/bin/stackk", out); err != nil { // second run: no change
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var s map[string]any
	json.Unmarshal(raw, &s)
	if s["model"] != "opus" {
		t.Error("lost other settings")
	}
	stop := s["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || !strings.Contains(string(raw), "other-tool") {
		t.Errorf("Stop hooks: %v", stop)
	}
	if strings.Count(string(raw), "stackk mark claude") != len(claudeEvents) {
		t.Errorf("want one stackk hook per event:\n%s", raw)
	}
	if !strings.Contains(strings.Join(log, "\n"), "already") {
		t.Error("second run should report no change")
	}
}

func TestOmpExtensionGetsBinaryPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent", "extensions")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "stackk.ts")
	if err := installOmpExtension(path, "/opt/stackk", func(string) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"/opt/stackk"`) || strings.Contains(string(raw), "__STACKK_BIN__") {
		t.Error("binary path not filled in")
	}
}

func TestClaudeSettingsKeepTheirMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{}`), 0o600)
	if err := installClaudeHooks(path, "/bin/stackk", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("settings.json became %v", st.Mode().Perm())
	}
}
