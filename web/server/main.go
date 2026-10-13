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
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// static/app is the built web app (make web); static/.gitkeep keeps the embed valid before that.
//
//go:embed all:static
var static embed.FS

var (
	addr    = flag.String("addr", "127.0.0.1:7878", "listen address; keep it on localhost and publish it with `tailscale serve`")
	stackk  = flag.String("stackk", "", "the stackk binary (default: stackk in PATH, then ~/.local/bin/stackk)")
	allow   = flag.String("allow", "", "with tailscale serve: the only Tailscale login let in (e.g. you@gmail.com)")
	devices = flag.String("devices", "", "the only tailnet devices let in, by name or Tailscale IP (e.g. iphone-15); comma-separated")
)

func main() {
	flag.Parse()
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	bin, err := findStackk(*stackk)
	if err != nil {
		log.Fatal(err)
	}
	s := &server{bin: bin, allow: *allow}
	if *devices != "" {
		if s.devices, err = resolveDevices(*devices); err != nil {
			log.Fatal(err)
		}
		log.Printf("only these devices are let in: %s", *devices)
	}
	go s.reconcile()
	// Take the reconcile process along when stopped.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-stop
		if p := s.reconciling.Load(); p != nil {
			p.Kill()
		}
		os.Exit(0)
	}()
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
	reconciling atomic.Pointer[os.Process]
	bin         string
	allow       string
	devices     map[string]bool // tailnet IPs; empty: any device
}

// resolveDevices turns device names (MagicDNS) or IPs into the set of IPs let in.
func resolveDevices(list string) (map[string]bool, error) {
	ips := map[string]bool{}
	for _, d := range strings.Split(list, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if ip := net.ParseIP(d); ip != nil {
			ips[ip.String()] = true
			continue
		}
		addrs, err := net.LookupHost(d)
		if err != nil {
			return nil, fmt.Errorf("device %q: %v (is MagicDNS on? an IP works too)", d, err)
		}
		for _, a := range addrs {
			ips[a] = true
		}
	}
	return ips, nil
}

// reconcile keeps the stack's state current when no dashboard runs (that is who usually does
// it): agents that exited, panes that are gone, windows added by hand, agents gone quiet. One
// long-running `stackk reconcile -every` remembers screens between rounds; restarted if it dies.
func (s *server) reconcile() {
	for {
		cmd := exec.Command(s.bin, "reconcile", "-every", "10s")
		cmd.Stderr = os.Stderr
		err := cmd.Start()
		if err == nil {
			s.reconciling.Store(cmd.Process)
			err = cmd.Wait()
		}
		if err != nil {
			log.Printf("reconcile: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
}

// origin is where a request comes from. A connection from this Mac is either tailscale serve,
// which names the device (X-Forwarded-For) and you (Tailscale-User-Login), or the Mac itself.
// Anything else came over the network directly: its address is real, any identity header isn't.
type origin struct {
	ip      string
	local   bool // the Mac itself, not through tailscale serve
	proxied bool // through tailscale serve: its headers can be trusted
}

func originOf(r *http.Request) origin {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a := net.ParseIP(host); a != nil && a.IsLoopback() {
		if fwd := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); fwd != "" {
			return origin{ip: fwd, proxied: true}
		}
		return origin{ip: host, local: true}
	}
	return origin{ip: host}
}

// hostAllowed refuses requests addressed to any other name: a page on some site could otherwise
// point its own domain at 127.0.0.1 (DNS rebinding) and drive the agents from your browser.
func hostAllowed(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".ts.net") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && (ip.IsLoopback() || tailnetIP(ip))
}

// tailnetIP is an address Tailscale hands out (100.64.0.0/10, fd7a:115c:a1e0::/48).
func tailnetIP(ip net.IP) bool {
	_, v4, _ := net.ParseCIDR("100.64.0.0/10")
	_, v6, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return v4.Contains(ip) || v6.Contains(ip)
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks", s.tasks)
	mux.HandleFunc("GET /api/dirs", s.dirs)
	mux.HandleFunc("GET /api/agents", s.agents)
	mux.HandleFunc("POST /api/new", s.newTask)
	mux.HandleFunc("GET /api/tasks/{id}/screen", s.screen)
	mux.HandleFunc("GET /api/tasks/{id}/transcript", s.transcript)
	mux.HandleFunc("POST /api/tasks/{id}/send", s.send)
	mux.HandleFunc("POST /api/tasks/{id}/key", s.key)
	mux.HandleFunc("POST /api/tasks/{id}/upload", s.upload)
	mux.HandleFunc("POST /api/tasks/{id}/rename", s.rename)
	mux.HandleFunc("POST /api/tasks/{id}/{action}", s.action)
	app, _ := fs.Sub(static, "static/app")
	mux.Handle("GET /", spa(http.FS(app)))
	return s.guard(mux)
}

// guard lets in only who may drive your terminals: with tailscale serve, the allowed login; and
// no cross-site POSTs (a page elsewhere must not type into your agents).
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host) {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		// The Mac itself (a browser on localhost) can run stackk anyway. Anything else must be a
		// listed device, and with -allow your login, which only tailscale serve can vouch for.
		if o := originOf(r); !o.local {
			if s.allow != "" && (!o.proxied || r.Header.Get("Tailscale-User-Login") != s.allow) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if len(s.devices) > 0 && !s.devices[o.ip] {
				http.Error(w, "this device is not allowed", http.StatusForbidden)
				return
			}
		}
		if r.Method != http.MethodGet {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
			// JSON, or raw bytes for an upload: neither can come from a plain cross-site form
			// (both make the browser ask first, and this server never says yes).
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") && ct != "application/octet-stream" {
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
	idRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)
	keyRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,15}$`)
	waitingRe = regexp.MustCompile(`^[0-9]{1,12}-[0-9a-f]{8}$`)
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

func (s *server) transcript(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	n := 60
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 && v <= 1000 {
		n = v
	}
	out, err := s.run(r.Context(), "", "transcript", "-json", "-n", strconv.Itoa(n), id)
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
		Keys      []string `json:"keys"`
		IfWaiting string   `json:"if_waiting"` // the prompt the answer is for (task waiting_id)
		IfTrust   bool     `json:"if_trust"`   // an answer to "do you trust this folder?"
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
	args := []string{"key"}
	if body.IfWaiting != "" {
		if !waitingRe.MatchString(body.IfWaiting) {
			http.Error(w, "bad if_waiting", http.StatusBadRequest)
			return
		}
		args = append(args, "-if-waiting", body.IfWaiting)
	}
	if body.IfTrust {
		args = append(args, "-if-trust")
	}
	out, err := s.run(r.Context(), "", append(append(args, id), body.Keys...)...)
	s.reply(w, out, err)
}

// rename passes the new name on stdin: whatever it is, it can't become a flag.
func (s *server) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, "bad JSON: want {\"name\": \"...\"}", http.StatusBadRequest)
		return
	}
	out, err := s.run(r.Context(), body.Name, "rename", id)
	s.reply(w, out, err)
}

// upload hands a file from the phone to `stackk upload`, which saves it where the agent can
// read it and answers with its path.
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" || strings.HasPrefix(name, "-") || len(name) > 200 {
		http.Error(w, "bad file name", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if err != nil {
		http.Error(w, "file too large (25 MB at most)", http.StatusRequestEntityTooLarge)
		return
	}
	out, err := s.run(r.Context(), string(body), "upload", "-json", id, name)
	s.reply(w, out, err)
}

// folder accepts an absolute path (never one that looks like a flag); "" is your home.
func folder(p string) bool {
	return p == "" || strings.HasPrefix(p, "/") && len(p) < 1024 && !strings.ContainsRune(p, 0)
}

func (s *server) dirs(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !folder(p) {
		http.Error(w, "bad folder", http.StatusBadRequest)
		return
	}
	args := []string{"dirs", "-json"}
	if p != "" {
		args = append(args, p)
	}
	out, err := s.run(r.Context(), "", args...)
	s.reply(w, out, err)
}

// agents lists the agents stackk can start, the default first.
func (s *server) agents(w http.ResponseWriter, r *http.Request) {
	out, err := s.run(r.Context(), "", "agents", "-json")
	s.reply(w, out, err)
}

var (
	stackRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	agentRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`) // stackk new refuses one it doesn't know
)

// newTask starts a session in a folder picked with dirs: `stackk dirs` vets it first (only your
// home folder), then `stackk new` starts the agent.
func (s *server) newTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cwd    string `json:"cwd"`
		Prompt string `json:"prompt"`
		Agent  string `json:"agent"`
		Stack  string `json:"stack"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	if body.Cwd == "" || !folder(body.Cwd) || (body.Agent != "" && !agentRe.MatchString(body.Agent)) ||
		(body.Stack != "" && !stackRe.MatchString(body.Stack)) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := s.run(r.Context(), "", "dirs", body.Cwd); err != nil {
		s.reply(w, nil, err)
		return
	}
	args := []string{"new"}
	if body.Agent != "" {
		args = append(args, "-agent", body.Agent)
	}
	if body.Stack != "" {
		args = append(args, "-stack", body.Stack)
	}
	args = append(args, body.Cwd)
	if p := strings.TrimSpace(body.Prompt); p != "" {
		args = append(args, p) // after the folder: positional, whatever it starts with
	}
	out, err := s.run(r.Context(), "", args...)
	if err != nil {
		s.reply(w, nil, err)
		return
	}
	b, _ := json.Marshal(map[string]string{"id": strings.TrimSpace(string(out))})
	s.reply(w, b, nil)
}

// action runs the task commands that take nothing else: pause, resume, hide, unhide, priority,
// unpriority and rm (close the window, remove the task for good).
func (s *server) action(w http.ResponseWriter, r *http.Request) {
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	switch a := r.PathValue("action"); a {
	case "pause", "resume", "hide", "unhide", "priority", "unpriority", "rm":
		out, err := s.run(r.Context(), "", a, id)
		s.reply(w, out, err)
	default:
		http.NotFound(w, r)
	}
}
