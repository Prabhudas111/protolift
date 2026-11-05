// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package webui is the local control screen: boxes, spend, budgets and stop
// settings, on this computer only. It is a driving adapter: it uses only the
// env facade.
//
// Safety: it listens on 127.0.0.1 only; every request needs the random token
// from the link (compared in constant time); the Host header must be the
// loopback address, which blocks DNS-rebinding pages; nothing is allowed
// cross-origin; the page loads nothing from outside.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

//go:embed static/*
var static embed.FS

// Limits.
const (
	readTimeout   = 15 * time.Second
	statusTimeout = 30 * time.Second
	maxBody       = 16 << 10
)

// Server serves the control screen.
type Server struct {
	open  env.Opener
	token string
	addr  string
	mu    sync.Mutex
	jobs  map[string]*job // project key → the start running for it
}

type job struct {
	Running bool   `json:"running"`
	Log     string `json:"log"`
	Error   string `json:"error,omitempty"`
}

// New prepares a server on 127.0.0.1:port (0 picks a free port).
func New(open env.Opener, port int) (*Server, net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, nil, errs.Wrap(errs.CodePortBusy, err, fmt.Sprintf("port %d is in use", port), "pick another with --port, or leave it out to use any free port")
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, nil, errs.Wrap(errs.CodeInternal, err, "cannot make a secret key", "")
	}
	return &Server{open: open, token: hex.EncodeToString(b), addr: ln.Addr().String(), jobs: map[string]*job{}}, ln, nil
}

// URL is the link to open, with the token.
func (s *Server) URL() string { return "http://" + s.addr + "/?t=" + s.token }

// Serve runs until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page("static/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.page("static/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.css", s.page("static/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /api/state", s.api(s.state))
	mux.HandleFunc("POST /api/start", s.api(s.start))
	mux.HandleFunc("POST /api/stop", s.api(s.stop))
	mux.HandleFunc("POST /api/budget", s.api(s.budget))
	mux.HandleFunc("POST /api/stops", s.api(s.stops))
	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: readTimeout, ReadTimeout: readTimeout}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut) // gate-allow: shutting down on Ctrl-C; an error here has no one left to report to
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errs.Wrap(errs.CodeInternal, err, "the control screen stopped", "")
	}
	return nil
}

// publicAsset are the files the page loads by itself (no token on them).
var publicAsset = map[string]bool{"/app.js": true, "/app.css": true}

// guard checks the host on every request, the token on everything but the
// public assets, and sets strict headers.
func (s *Server) guard(next http.Handler) http.Handler {
	port := s.addr[strings.LastIndex(s.addr, ":"):]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1"+port && r.Host != "localhost"+port {
			http.Error(w, "wrong host", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Protolift-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		// The page's script and styles are the same public code for everyone
		// and hold no data, so the browser may fetch them without the token.
		// The page itself, all data and all actions need it.
		if !publicAsset[r.URL.Path] && subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			http.Error(w, "open the link that `protolift ui` printed", http.StatusUnauthorized)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) page(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := static.ReadFile(name)
		if err != nil {
			http.Error(w, "missing page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b) // gate-allow: a failed write means the browser went away; nothing to do
	}
}

// api wraps a handler: JSON in, JSON out, errors in protolift's model.
func (s *Server) api(fn func(ctx context.Context, body []byte) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the request"})
			return
		}
		out, err := fn(r.Context(), body)
		if err != nil {
			e := errs.As(err)
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": e.Message, "fix": e.Fix, "code": string(e.Code)})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // gate-allow: a failed write means the browser went away; nothing to do
}

// target is how the page names a project.
type target struct {
	Dir   string `json:"dir"`
	Name  string `json:"envName"`
	Cloud string `json:"cloud"`
}

func (t target) key() string { return t.Dir + "\x00" + t.Name }

func decode(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return errs.Wrap(errs.CodeInvalidInput, err, "the request is not valid", "reload the page")
	}
	return nil
}

// quiet is the UI for screen actions: it keeps the log for the page and
// says yes, because the person pressed the button that asked.
type quiet struct {
	mu  sync.Mutex
	log strings.Builder
}

func (q *quiet) add(p, m string)     { q.mu.Lock(); q.log.WriteString(p + m + "\n"); q.mu.Unlock() }
func (q *quiet) Step(m string)       { q.add("→ ", m) }
func (q *quiet) Info(m string)       { q.add("  ", m) }
func (q *quiet) Warn(m string)       { q.add("! ", m) }
func (q *quiet) Confirm(string) bool { return true }
func (q *quiet) Out() io.Writer      { return io.Discard }
func (q *quiet) text() string        { q.mu.Lock(); defer q.mu.Unlock(); return q.log.String() }

type projectState struct {
	env.CostLine
	Box   *env.Box `json:"box"`
	Price string   `json:"price,omitempty"`
	Ports []int    `json:"ports"`
	Error string   `json:"error,omitempty"`
	Job   *job     `json:"job,omitempty"`
}

func (s *Server) state(ctx context.Context, _ []byte) (any, error) {
	m, err := s.open(ctx, env.OpenRequest{Dir: ".", UI: &quiet{}})
	if err != nil {
		return nil, err
	}
	report, err := m.Cost(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]projectState, len(report.Lines))
	var wg sync.WaitGroup
	for i, l := range report.Lines {
		out[i] = projectState{CostLine: l}
		wg.Add(1)
		go func(i int, l env.CostLine) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, statusTimeout)
			defer cancel()
			pm, err := s.open(c, env.OpenRequest{Dir: l.Dir, Name: l.EnvName, Cloud: l.Provider, UI: &quiet{}})
			if err == nil {
				var r env.StatusReport
				if r, err = pm.Status(c, env.Target{Dir: l.Dir, Name: l.EnvName}); err == nil {
					out[i].Box, out[i].Price, out[i].Ports = r.Box, r.Price, r.Ports
				}
			}
			if err != nil {
				out[i].Error = errs.As(err).Message
			}
		}(i, l)
	}
	wg.Wait()
	s.mu.Lock()
	for i := range out {
		if j, ok := s.jobs[target{Dir: out[i].Dir, Name: out[i].EnvName}.key()]; ok {
			cp := *j
			out[i].Job = &cp
		}
	}
	s.mu.Unlock()
	return map[string]any{"projects": out, "todayUsd": report.Today, "monthUsd": report.Month, "totalBudget": report.Global, "note": report.Note}, nil
}

// start runs `up --detach` in the background and reports through jobs.
func (s *Server) start(_ context.Context, body []byte) (any, error) {
	var t target
	if err := decode(body, &t); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if j, ok := s.jobs[t.key()]; ok && j.Running {
		s.mu.Unlock()
		return nil, errs.New(errs.CodeConflict, "this box is already starting", "wait for it to finish")
	}
	j := &job{Running: true}
	s.jobs[t.key()] = j
	s.mu.Unlock()
	go func() {
		ui := &quiet{}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		m, err := s.open(ctx, env.OpenRequest{Dir: t.Dir, Name: t.Name, Cloud: t.Cloud, UI: ui})
		if err == nil {
			err = m.Up(ctx, env.UpRequest{Target: env.Target{Dir: t.Dir, Name: t.Name}, Detached: true})
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		j.Running, j.Log = false, ui.text()
		if err != nil {
			e := errs.As(err)
			j.Error = e.Message + ". " + e.Fix
		}
	}()
	return map[string]bool{"started": true}, nil
}

func (s *Server) stop(ctx context.Context, body []byte) (any, error) {
	var t target
	if err := decode(body, &t); err != nil {
		return nil, err
	}
	m, err := s.open(ctx, env.OpenRequest{Dir: t.Dir, Name: t.Name, Cloud: t.Cloud, UI: &quiet{}})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"stopped": true}, m.Stop(ctx, env.Target{Dir: t.Dir, Name: t.Name})
}

func (s *Server) budget(ctx context.Context, body []byte) (any, error) {
	var in struct {
		target
		All        bool    `json:"all"`
		DailyUSD   float64 `json:"dailyUsd"`
		MonthlyUSD float64 `json:"monthlyUsd"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	m, err := s.open(ctx, env.OpenRequest{Dir: orDot(in.Dir), Name: in.Name, Cloud: in.Cloud, UI: &quiet{}})
	if err != nil {
		return nil, err
	}
	b := env.Budget{DailyUSD: in.DailyUSD, MonthlyUSD: in.MonthlyUSD}
	if in.All {
		return map[string]bool{"saved": true}, m.SetGlobalBudget(b)
	}
	return map[string]bool{"saved": true}, m.SetBudget(env.Target{Dir: in.Dir, Name: in.Name}, b)
}

func (s *Server) stops(ctx context.Context, body []byte) (any, error) {
	var in struct {
		target
		IdleMinutes int `json:"idleMinutes"`
		MaxHours    int `json:"maxHours"`
	}
	if err := decode(body, &in); err != nil {
		return nil, err
	}
	m, err := s.open(ctx, env.OpenRequest{Dir: in.Dir, Name: in.Name, Cloud: in.Cloud, UI: &quiet{}})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"saved": true}, m.SetStops(env.Target{Dir: in.Dir, Name: in.Name}, in.IdleMinutes, in.MaxHours)
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}
