// Package agent knows each coding agent stackk runs: how to start and resume it, how it is wired
// to report to stackk, where its conversations live and which keys answer its prompts. The rest
// of stackk works on one shared model (tasks, phases, hook-shaped events) and asks the agent
// through this interface. What an agent can't do is ErrNotImplemented or an empty answer, and
// callers degrade instead of guessing.
package agent

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/krol22/stackk/cli/internal/sessions"
)

var ErrNotImplemented = errors.New("not implemented for this agent")

type Agent interface {
	Name() string
	// Detect reports whether a pane's foreground command is this agent.
	Detect(command string) bool
	// Argv starts the agent: on a new conversation with prompt, or continuing the one ref names.
	Argv(prompt, ref string) ([]string, error)
	// ResumeRef is the ref Argv continues a task's conversation from ("" when it can't).
	ResumeRef(sessionID, sessionPath string) string
	// Install wires the agent to report to stackk by running `self mark <name>`.
	Install(home, self string, out func(string)) error
	// SessionFiles lists its conversation files modified after cutoff.
	SessionFiles(cutoff time.Time) []sessions.File
	// ParseSession reads one conversation for search.
	ParseSession(f sessions.File) (sessions.Session, bool)
	// Messages reads the last limit messages of a transcript.
	Messages(path string, limit int) ([]sessions.Message, error)
	// MainSession maps a subagent's transcript to its parent's; any other path comes back as is.
	MainSession(path string) (main string, subagent bool)
	// LastText is the agent's last answer from its transcript, for hooks that don't carry it.
	LastText(path string) string
	// AsksTrust reports the agent's "do you trust this folder?" on its screen.
	AsksTrust(screen string) bool
	// Answers are the keys that answer its prompts; a question has n options.
	Answers(n int) Answers
	// CancelReported tells whether cancelling a prompt (Esc) reaches stackk as an event; when it
	// doesn't, stackk marks the task idle itself.
	CancelReported() bool
}

// Answers are key sequences for `stackk key`; an empty one is an answer the agent doesn't offer.
type Answers struct {
	Options [][]string `json:"options,omitempty"` // a question's option i
	Own     []string   `json:"own,omitempty"`     // a question: then type your own answer
	Steps   []string   `json:"steps,omitempty"`   // several questions or answers: the keys that step through them
	Yes     []string   `json:"yes,omitempty"`     // a permission
	Always  []string   `json:"always,omitempty"`
	No      []string   `json:"no,omitempty"`
	Cancel  []string   `json:"cancel,omitempty"`
	Trust   []string   `json:"trust,omitempty"` // "do you trust this folder?"
	Exit    []string   `json:"exit,omitempty"`
}

var all = []Agent{claude{base{"claude"}}, omp{base{"omp"}}}

// All lists the agents stackk knows, the default first.
func All() []Agent { return all }

// Names lists their names, the default first.
func Names() []string {
	var out []string
	for _, a := range all {
		out = append(out, a.Name())
	}
	return out
}

func Get(name string) (Agent, error) {
	for _, a := range all {
		if a.Name() == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %q (one of: %s)", name, strings.Join(Names(), ", "))
}

// Of names the agent a pane's foreground command is, "" for anything else (a shell).
func Of(command string) string {
	for _, a := range all {
		if a.Detect(command) {
			return a.Name()
		}
	}
	return ""
}

// Files lists every agent's conversation files, newest first, modified within maxAge.
func Files(maxAge time.Duration, limit int) []sessions.File {
	var out []sessions.File
	cutoff := time.Now().Add(-maxAge)
	for _, a := range all {
		out = append(out, a.SessionFiles(cutoff)...)
	}
	return sessions.Newest(out, limit)
}

// Parse reads one conversation file with its agent's reader.
func Parse(f sessions.File) (sessions.Session, bool) {
	a, err := Get(f.Agent)
	if err != nil {
		return sessions.Session{}, false
	}
	return a.ParseSession(f)
}

// base is what an agent does unless it says otherwise: start as `<name> [--resume ref] [prompt]`,
// and nothing more.
type base struct{ name string }

func (b base) Name() string               { return b.name }
func (b base) Detect(command string) bool { return command == b.name }

func (b base) Argv(prompt, ref string) ([]string, error) {
	bin, err := exec.LookPath(b.name)
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH", b.name)
	}
	argv := []string{bin}
	if ref != "" {
		argv = append(argv, "--resume", ref)
	}
	if prompt != "" {
		if strings.HasPrefix(prompt, "-") { // a prompt, never an option of the agent's
			prompt = " " + prompt
		}
		argv = append(argv, prompt)
	}
	return argv, nil
}

func (base) ResumeRef(sessionID, _ string) string                { return sessionID }
func (base) Install(string, string, func(string)) error          { return ErrNotImplemented }
func (base) SessionFiles(time.Time) []sessions.File              { return nil }
func (base) ParseSession(sessions.File) (sessions.Session, bool) { return sessions.Session{}, false }
func (base) Messages(string, int) ([]sessions.Message, error)    { return nil, ErrNotImplemented }
func (base) MainSession(path string) (string, bool)              { return path, false }
func (base) LastText(string) string                              { return "" }
func (base) AsksTrust(string) bool                               { return false }
func (base) Answers(int) Answers                                 { return Answers{} }
func (base) CancelReported() bool                                { return true }
