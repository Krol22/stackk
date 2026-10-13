package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Hit is a search result.
type Hit struct {
	Session
	Score float64 `json:"score"`
	AI    bool    `json:"ai"` // ranked by the model
}

func terms(q string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	}) {
		if len([]rune(f)) >= 3 { // "do", "na", "w", "to": noise in any language
			out = append(out, f)
		}
	}
	return out
}

// HasTerms reports whether the query has any word long enough to search for.
func HasTerms(q string) bool { return len(terms(q)) > 0 }

// Local ranks sessions by keyword matches (title counts most), then recency. An empty query
// lists the most recent sessions.
func Local(all []Session, query string, limit int) []Hit {
	ts := terms(query)
	var hits []Hit
	for _, s := range all {
		score := 0.0
		title, cwd := strings.ToLower(s.Title), strings.ToLower(filepath.Base(s.Cwd))
		prompts := strings.ToLower(strings.Join(s.Prompts, " "))
		last := strings.ToLower(s.LastText)
		for _, t := range ts {
			switch {
			case strings.Contains(title, t):
				score += 5
			case strings.Contains(cwd, t):
				score += 3
			case strings.Contains(prompts, t):
				score += 2
			case strings.Contains(last, t):
				score += 1
			}
		}
		if len(ts) > 0 && score == 0 {
			continue
		}
		// Newer wins among equals: up to +1 for sessions from the last week.
		age := time.Since(s.Modified).Hours() / 24
		score += max(0, 1-age/7)
		hits = append(hits, Hit{Session: s, Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

var aiTimeout = 45 * time.Second

// AI asks a small model to pick and order the sessions matching the query, from candidates: the
// local keyword hits first, topped up with recent sessions so a match in other words still
// has a chance (e.g. a Polish query for an English session).
func AI(all []Session, query string, limit int) ([]Hit, error) {
	pool := Local(all, query, 25)
	seen := map[string]bool{}
	for _, h := range pool {
		seen[h.Path] = true
	}
	recent := append([]Session(nil), all...)
	sort.Slice(recent, func(i, j int) bool { return recent[i].Modified.After(recent[j].Modified) })
	for _, s := range recent {
		if len(pool) >= 50 {
			break
		}
		if !seen[s.Path] {
			pool = append(pool, Hit{Session: s})
		}
	}
	if len(pool) == 0 {
		return nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You help find a past coding-agent session. The user searches for: %q\n\n", query)
	b.WriteString("Sessions (number. title | folder | date | first prompt | last prompt):\n")
	for i, h := range pool {
		first, last := "", ""
		if len(h.Prompts) > 0 {
			first, last = h.Prompts[0], h.Prompts[len(h.Prompts)-1]
		}
		fmt.Fprintf(&b, "%d. %s | %s | %s | %s | %s\n", i+1, Clip(h.Title, 80), filepath.Base(h.Cwd), h.Modified.Format("01-02"),
			Clip(first, 110), Clip(last, 80))
	}
	fmt.Fprintf(&b, "\nReply with only a JSON array of the numbers of sessions that match, best match first, at most %d. Reply [] if none match.\n", limit)
	out, err := runModel(b.String())
	if err != nil {
		return nil, err
	}
	ids, err := parseNumbers(out)
	if err != nil {
		return nil, fmt.Errorf("AI reply: %v", err)
	}
	var hits []Hit
	for _, n := range ids {
		if n >= 1 && n <= len(pool) && len(hits) < limit {
			h := pool[n-1]
			h.AI = true
			hits = append(hits, h)
		}
	}
	return hits, nil
}

// runModel sends the prompt on stdin to $STACKK_AI_CMD, or to a one-off `claude -p` on a small
// model that saves no session and runs none of the user's hooks.
func runModel(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if c := os.Getenv("STACKK_AI_CMD"); c != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", c)
	} else {
		bin, err := exec.LookPath("claude")
		if err != nil {
			return "", fmt.Errorf("claude not found in PATH")
		}
		// No tools, no MCP servers, no saved session: a plain one-shot ranking call.
		dir, err := aiWorkDir()
		if err != nil {
			return "", err
		}
		cmd = exec.CommandContext(ctx, bin, "-p", "--model", "haiku", "--no-session-persistence",
			"--setting-sources", "project", "--strict-mcp-config", "--tools=", "--output-format", "text")
		cmd.Dir = dir
	}
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("AI search timed out")
		}
		return "", fmt.Errorf("AI search failed: %v %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// aiWorkDir is an empty directory only you can write to. The model call reads project settings
// (and CLAUDE.md) from its working directory, so it must not run anywhere others can plant them,
// like a shared /tmp.
func aiWorkDir() (string, error) {
	base := os.Getenv("STACKK_DB")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state", "stackk", "x")
	}
	dir := filepath.Join(filepath.Dir(base), "ai")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	// Nothing may sit in it that claude would load.
	for _, name := range []string{".claude", "CLAUDE.md", ".mcp.json"} {
		os.RemoveAll(filepath.Join(dir, name))
	}
	return dir, nil
}

var arrayRe = regexp.MustCompile(`\[[\d\s,]*\]`)

func parseNumbers(s string) ([]int, error) {
	m := arrayRe.FindString(s)
	if m == "" {
		return nil, fmt.Errorf("no JSON array in %q", Clip(s, 120))
	}
	var ids []int
	return ids, json.Unmarshal([]byte(m), &ids)
}
