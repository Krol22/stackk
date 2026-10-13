// Package setup wires stackk into every agent it knows and installs the stackk skill.
package setup

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/krol22/stackk/cli/internal/agent"
)

//go:embed skill.md
var skill string

// Run installs every integration and reports what it changed.
func Run(self string, out func(string)) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, a := range agent.All() {
		switch err := a.Install(home, self, out); {
		case errors.Is(err, agent.ErrNotImplemented):
			out(a.Name() + ": can't report to stackk yet, its sessions show what the screen tells")
		case err != nil:
			return fmt.Errorf("%s: %w", a.Name(), err)
		}
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
