package core

import (
	"testing"

	"github.com/krol22/stackk/cli/internal/store"
)

func TestSplitName(t *testing.T) {
	cases := []struct {
		in, base          string
		scheduled, paused bool
	}{
		{"yum-3597 st", "yum-3597", true, false},
		{"yum-3597 st st", "yum-3597", true, false},
		{"cp-chat p p", "cp-chat", false, true},
		{"cp-chat p st", "cp-chat", true, false},
		{"rich-panel-snooze", "rich-panel-snooze", false, false},
		{"st", "st", false, false},
	}
	for _, c := range cases {
		base, sch, p := SplitName(c.in)
		if base != c.base || sch != c.scheduled || p != c.paused {
			t.Errorf("SplitName(%q) = %q %v %v", c.in, base, sch, p)
		}
	}
}

func TestWindowNameNeverDoublesSuffix(t *testing.T) {
	if got := WindowName(store.Task{Name: "yum-3597 st", Deferred: true}); got != "yum-3597 st" {
		t.Errorf("got %q", got)
	}
	if got := WindowName(store.Task{Name: "cp-chat p", PausedAt: 1}); got != "cp-chat p" {
		t.Errorf("got %q", got)
	}
	if got := WindowName(store.Task{Name: "x st"}); got != "x" {
		t.Errorf("got %q", got)
	}
}

func TestShellArgsNeverQuoteThePrompt(t *testing.T) {
	env, cmd := shellArgs([]string{"/bin/claude", `it's a "test" \ $HOME`})
	if cmd != `exec "$STACKK_A0" "$STACKK_A1"` {
		t.Errorf("command %s", cmd)
	}
	if env[3] != `STACKK_A1=it's a "test" \ $HOME` {
		t.Errorf("env %q", env)
	}
}
