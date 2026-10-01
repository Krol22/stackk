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
	out("restart running Claude / omp sessions (/exit, then claude -c / omp -c) so they report to stackk")
	return nil
}

func claude(path, self string, out func(string)) error {
	settings := map[string]any{}
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
	if err := writeAtomic(path, append(data, '\n')); err != nil {
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
			if c, _ := hm["command"].(string); strings.Contains(c, "stackk mark claude") {
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
	if err := writeAtomic(path, []byte(want)); err != nil {
		return err
	}
	out("omp: extension written to " + path)
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
