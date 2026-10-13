// Package sessions holds what stackk knows about agents' conversations on disk, whatever the
// agent: sessions for search, transcripts as messages, and the search itself. The agent package
// reads each agent's own files into these.
package sessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/krol22/stackk/cli/internal/safe"
)

// Session is what search needs to know about one past conversation.
type Session struct {
	Agent    string    `json:"agent"`
	ID       string    `json:"id"`
	Path     string    `json:"path"` // transcript / session file
	Cwd      string    `json:"cwd"`
	Title    string    `json:"title"`
	Prompts  []string  `json:"prompts"` // first and last user prompts, trimmed
	LastText string    `json:"last_text"`
	Modified time.Time `json:"modified"`
}

// File is a session file found on disk, before parsing.
type File struct {
	Agent string
	Path  string
	Mod   time.Time
	Size  int64
}

// Glob lists an agent's session files matching pattern, modified after cutoff.
func Glob(agent, pattern string, cutoff time.Time) []File {
	var out []File
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.IsDir() || st.ModTime().Before(cutoff) {
			continue
		}
		out = append(out, File{Agent: agent, Path: m, Mod: st.ModTime(), Size: st.Size()})
	}
	return out
}

// Newest sorts files newest first and keeps at most limit.
func Newest(files []File, limit int) []File {
	sort.Slice(files, func(i, j int) bool { return files[i].Mod.After(files[j].Mod) })
	if len(files) > limit {
		files = files[:limit]
	}
	return files
}

// Read parses one session file line by line with the agent's line reader, which fills in the
// session and collects the user's prompts.
func Read(f File, line func(l []byte, s *Session, prompts *[]string)) (Session, bool) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return Session{}, false
	}
	defer fh.Close()
	s := Session{Agent: f.Agent, Path: f.Path, Modified: f.Mod}
	var prompts []string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line(sc.Bytes(), &s, &prompts)
	}
	if s.ID == "" || scratch(s.Cwd) {
		return s, false
	}
	s.Prompts = firstLast(prompts, 3, 5)
	s.Title = Clip(s.Title, 200)
	if s.Title == "" && len(prompts) > 0 {
		s.Title = Clip(prompts[0], 80)
	}
	return s, true
}

// scratch reports throwaway folders (test runs, temp dirs): not sessions you'd come back to.
func scratch(cwd string) bool {
	if os.Getenv("STACKK_INDEX_TMP") != "" { // tests keep their sessions in temp dirs
		return false
	}
	for _, p := range []string{"/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/"} {
		if strings.HasPrefix(cwd+"/", p) {
			return true
		}
	}
	return false
}

// UserText filters out tool results, slash-command echoes and injected reminders.
func UserText(t string) bool {
	t = strings.TrimSpace(t)
	return t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "[Request interrupted")
}

func BlocksText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, " ")
}

func firstLast(xs []string, first, last int) []string {
	if len(xs) <= first+last {
		return xs
	}
	return append(append([]string{}, xs[:first]...), xs[len(xs)-last:]...)
}

func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(safe.Text(s)), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
