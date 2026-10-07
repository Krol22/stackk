// Package setup wires stackk into Claude Code (hooks) and omp (an extension).
package setup

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed skill.md
var skill string

//go:embed omp_extension.ts
var ompExtension string

// Claude Code events stackk listens to. PreToolUse only for AskUserQuestion (a question to you).
var claudeEvents = []struct{ event, matcher string }{
	{"SessionStart", ""}, {"UserPromptSubmit", ""}, {"Stop", ""}, {"StopFailure", ""},
	{"PermissionRequest", ""}, {"PreToolUse", "AskUserQuestion"}, {"PostToolUse", ""},
	{"PostToolUseFailure", ""}, {"SessionEnd", ""},
}

// Run installs both integrations and reports what it changed.
func Run(self string, out func(string)) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := claude(filepath.Join(home, ".claude", "settings.json"), self, out); err != nil {
		return fmt.Errorf("claude hooks: %w", err)
	}
	if err := omp(filepath.Join(home, ".omp", "agent", "extensions", "stackk.ts"), self, out); err != nil {
		return fmt.Errorf("omp extension: %w", err)
	}
	// The skill that lets an agent start stackk sessions: Claude reads ~/.claude/skills, omp ~/.agents/skills.
	for _, dir := range []string{filepath.Join(home, ".claude", "skills", "stackk"), filepath.Join(home, ".agents", "skills", "stackk")} {
		if err := InstallSkill(dir); err != nil {
			return fmt.Errorf("skill: %w", err)
		}
		out("skill: " + filepath.Join(dir, "SKILL.md"))
	}
	out("restart running Claude / omp sessions (/exit, then claude -c / omp -c) so they report to stackk")
	return nil
}

// InstallSkill writes the stackk skill (SKILL.md) into dir.
func InstallSkill(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644)
}

func claude(path, self string, out func(string)) error {
	settings := map[string]any{}
	mode := os.FileMode(0o600) // a new settings file is private
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm() // keep whatever you chose
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	cmd := self + " mark claude"
	if strings.ContainsAny(self, " '\"") {
		cmd = "'" + strings.ReplaceAll(self, "'", `'"'"'`) + "' mark claude"
	}
	added := 0
	for _, e := range claudeEvents {
		entries, _ := hooks[e.event].([]any)
		if hasStackk(entries) {
			continue
		}
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": 3}}}
		if e.matcher != "" {
			entry["matcher"] = e.matcher
		}
		hooks[e.event] = append(entries, entry)
		added++
	}
	if added == 0 {
		out("claude: hooks already in " + path)
		return nil
	}
	settings["hooks"] = hooks
	if raw != nil {
		backup := path + ".bak-stackk-" + time.Now().Format("20060102150405")
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return err
		}
		out("claude: backup " + backup)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(data, '\n'), mode); err != nil {
		return err
	}
	out(fmt.Sprintf("claude: added %d hooks to %s", added, path))
	return nil
}

// hasStackk reports an existing stackk hook (any path to the binary) among an event's entries.
func hasStackk(entries []any) bool {
	for _, e := range entries {
		m, _ := e.(map[string]any)
		hs, _ := m["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); strings.Contains(c, "stackk") && strings.Contains(c, " mark claude") {
				return true
			}
		}
	}
	return false
}

func omp(path, self string, out func(string)) error {
	if _, err := os.Stat(filepath.Dir(filepath.Dir(path))); os.IsNotExist(err) {
		out("omp: not installed, skipped")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	want := strings.ReplaceAll(ompExtension, "__STACKK_BIN__", self)
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		out("omp: extension up to date at " + path)
		return nil
	}
	os.Remove(path) // may be a symlink from a manual install
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return err
	}
	out("omp: extension written to " + path)
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile's mode is cut by the umask
		return err
	}
	return os.Rename(tmp, path)
}
