// stackk-web serves the mobile web UI. It knows stackk only through its CLI: every request runs
// `stackk … -json`, so the web UI can do exactly what the command line can.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// static/app is the built web app (make web); static/.gitkeep keeps the embed valid before that.
//
//go:embed all:static
var static embed.FS

var (
	addr   = flag.String("addr", "127.0.0.1:7878", "listen address; keep it on localhost and publish it with `tailscale serve`")
	stackk = flag.String("stackk", "", "the stackk binary (default: stackk in PATH, then ~/.local/bin/stackk)")
	allow  = flag.String("allow", "", "with tailscale serve: the only Tailscale login let in (e.g. you@github)")
)

func main() {
	flag.Parse()
	bin, err := findStackk(*stackk)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{bin: bin, allow: *allow}
	log.Printf("stackk-web on http://%s (stackk: %s)", *addr, bin)
	log.Fatal(http.ListenAndServe(*addr, s.routes()))
}

func findStackk(given string) (string, error) {
	if given != "" {
		return given, nil
	}
	if p, err := exec.LookPath("stackk"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", "stackk")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("stackk not found: install it or pass -stackk")
	}
	return p, nil
}

type server struct {
	bin   string
	allow string
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks", s.tasks)
	mux.HandleFunc("GET /api/tasks/{id}/screen", s.screen)
	mux.HandleFunc("POST /api/tasks/{id}/send", s.send)
	mux.HandleFunc("POST /api/tasks/{id}/key", s.key)
	mux.HandleFunc("POST /api/tasks/{id}/{action}", s.action)
	app, _ := fs.Sub(static, "static/app")
	mux.Handle("GET /", spa(http.FS(app)))
	return s.guard(mux)
}

// guard lets in only who may drive your terminals: with tailscale serve, the allowed login; and
// no cross-site POSTs (a page elsewhere must not type into your agents).
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allow != "" && r.Header.Get("Tailscale-User-Login") != s.allow {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "JSON only", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// spa serves the built app, and index.html for any path it doesn't have (client-side routes).
func spa(files http.FileSystem) http.Handler {
	fileServer := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := files.Open(r.URL.Path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// run calls the stackk CLI. Arguments never come from the request unchecked: task ids and keys
// are validated, and free text goes through stdin.
func (s *server) run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimPrefix(strings.TrimSpace(errb.String()), "stackk: ")
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out.Bytes(), nil
}

// A task id and a key never start with "-", so neither can pass for a CLI flag.
var (
	idRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)
	keyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,15}$`)
)

func taskID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		http.Error(w, "bad task id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func (s *server) reply(w http.ResponseWriter, out []byte, err error) {
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if len(out) == 0 {
		out = []byte(`{"ok":true}`)
	}
	w.Write(out)
}

func (s *server) tasks(w http.ResponseWriter, r *http.Request) {
	out, err := s.run(r.Context(), "", "ls", "-json")
	s.reply(w, out, err)
}

func (s *server) screen(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	lines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n >= 0 && n <= 5000 {
		lines = n
	}
	out, err := s.run(r.Context(), "", "show", "-json", "-lines", strconv.Itoa(lines), id)
	s.reply(w, out, err)
}

func (s *server) send(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body struct {
		Text  string `json:"text"`
		Enter *bool  `json:"enter"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	args := []string{"send"}
	if body.Enter != nil && !*body.Enter {
		args = append(args, "-no-enter")
	}
	out, err := s.run(r.Context(), body.Text, append(args, id)...)
	s.reply(w, out, err)
}

func (s *server) key(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || len(body.Keys) == 0 {
		http.Error(w, "bad JSON: want {\"keys\": [...]}", http.StatusBadRequest)
		return
	}
	for _, k := range body.Keys {
		if !keyRe.MatchString(k) {
			http.Error(w, "bad key "+strconv.Quote(k), http.StatusBadRequest)
			return
		}
	}
	out, err := s.run(r.Context(), "", append([]string{"key", id}, body.Keys...)...)
	s.reply(w, out, err)
}

// action runs the task commands that take nothing else: pause, resume.
func (s *server) action(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	switch a := r.PathValue("action"); a {
	case "pause", "resume":
		out, err := s.run(r.Context(), "", a, id)
		s.reply(w, out, err)
	default:
		http.NotFound(w, r)
	}
}
