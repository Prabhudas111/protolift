// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Package mcpserver lets AI agents drive protolift over the Model Context
// Protocol (stdio). It is a driving adapter: it uses only the env facade.
//
// Safety rules for agents:
//   - Nothing that bills or deletes runs without an explicit confirm field,
//     and the tool descriptions tell the agent to ask its user first.
//   - Every answer includes the plain-words log, so the agent can relay it.
//   - Ports of a box started here stay mirrored until the session ends.
package mcpserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// Server serves protolift's tools.
type Server struct {
	open    env.Opener
	version string
	mu      sync.Mutex
	mirrors map[string]context.CancelFunc // project dir+name → its background mirror
}

// New returns a server that builds managers with open.
func New(open env.Opener, version string) *Server {
	return &Server{open: open, version: version, mirrors: map[string]context.CancelFunc{}}
}

// Project names the project a tool acts on.
type Project struct {
	Dir   string `json:"dir" jsonschema:"absolute path of the project folder on this computer"`
	Name  string `json:"name,omitempty" jsonschema:"optional second env name for the same folder"`
	Cloud string `json:"cloud,omitempty" jsonschema:"aws or gcp; empty uses what the project used before"`
}

// Empty is for tools without input.
type Empty struct{}

// ListInput picks the cloud and region to list; both optional.
type ListInput struct {
	Cloud  string `json:"cloud,omitempty" jsonschema:"aws or gcp; empty uses the default"`
	Region string `json:"region,omitempty" jsonschema:"region to list; empty uses your CLI default"`
}

// UpInput starts a box.
type UpInput struct {
	Project
	Size          string `json:"size,omitempty" jsonschema:"S, M, L or XL for a new box; empty lets protolift suggest one"`
	ConfirmCreate bool   `json:"confirm_create,omitempty" jsonschema:"must be true to create a NEW box (it costs money); ask the user first and tell them the price from the previous answer"`
}

// ConfirmInput is for tools that delete.
type ConfirmInput struct {
	Project
	Confirm bool `json:"confirm" jsonschema:"must be true; ask the user first, this deletes the box and its disk"`
}

// BudgetInput sets caps.
type BudgetInput struct {
	Project
	All        bool    `json:"all,omitempty" jsonschema:"true sets the total caps across every project instead of one project"`
	DailyUSD   float64 `json:"daily_usd" jsonschema:"daily cap in US dollars, 0 removes it"`
	MonthlyUSD float64 `json:"monthly_usd" jsonschema:"monthly cap in US dollars, 0 removes it"`
}

// Text is the plain answer every tool returns.
type Text struct {
	Log string `json:"log"`
}

func boolp(b bool) *bool { return &b }

// build registers every tool on a new MCP server.
func (s *Server) build() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "protolift", Version: s.version}, nil)
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_status", Annotations: read,
		Description: "Show one project's cloud box: state, size, price, cost so far, sync, ports, budget."}, s.status)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_list", Annotations: read,
		Description: "List every protolift box in the cloud and region."}, s.list)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_cost", Annotations: read,
		Description: "Estimated spend today and this month per project and in total, with budgets."}, s.cost)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_up", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(false), OpenWorldHint: boolp(true)},
		Description: "Start or wake the project's cloud box, sync files, start its stack, and mirror its ports to localhost for this session. " +
			"A NEW box costs money: the first call without confirm_create returns the price; ask the user, then call again with confirm_create=true."}, s.up)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_stop", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(false), IdempotentHint: true},
		Description: "Stop the project's box. Files and data stay on its disk; it stops billing for compute."}, s.stop)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_down", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(true)},
		Description: "Delete the project's box and its disk. Laptop files are not touched. Requires confirm=true; ask the user first."}, s.down)
	mcp.AddTool(srv, &mcp.Tool{Name: "protolift_set_budget", Annotations: &mcp.ToolAnnotations{DestructiveHint: boolp(false), IdempotentHint: true},
		Description: "Set daily and monthly spending caps in US dollars, for one project or (all=true) for every project together."}, s.setBudget)
	return srv
}

// Run serves on stdin/stdout until the client disconnects (Claude Code,
// Cursor, VS Code, Codex, Gemini CLI and other clients that start a process).
func (s *Server) Run(ctx context.Context) error {
	defer s.stopMirrors()
	return s.build().Run(ctx, &mcp.StdioTransport{})
}

// HTTP serves the MCP Streamable HTTP transport on 127.0.0.1, for clients
// that connect to a URL. Every request needs the bearer token; the Host must
// be the loopback address, which blocks DNS-rebinding web pages.
func (s *Server) HTTP(ctx context.Context, ln net.Listener, token string) error {
	defer s.stopMirrors()
	srv := s.build()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	port := ln.Addr().String()[strings.LastIndex(ln.Addr().String(), ":"):]
	guard := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1"+port && r.Host != "localhost"+port {
			http.Error(w, "wrong host", http.StatusForbidden)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "missing or wrong token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
	hs := &http.Server{Handler: guard, ReadHeaderTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut) // gate-allow: shutting down on Ctrl-C; an error here has no one left to report to
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// capture is the UI for one tool call: it records the log, and answers a
// confirmation with the confirm field the agent sent.
type capture struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	confirm bool
	asked   []string
}

func (c *capture) write(p, m string) { c.mu.Lock(); c.buf.WriteString(p + m + "\n"); c.mu.Unlock() }
func (c *capture) Step(m string)     { c.write("→ ", m) }
func (c *capture) Info(m string)     { c.write("  ", m) }
func (c *capture) Warn(m string)     { c.write("! ", m) }
func (c *capture) Out() io.Writer    { return &c.buf }
func (c *capture) Confirm(q string) bool {
	c.mu.Lock()
	c.asked = append(c.asked, q)
	c.mu.Unlock()
	return c.confirm
}

func (s *Server) manager(ctx context.Context, p Project, ui *capture) (*env.Manager, env.Target, error) {
	m, err := s.open(ctx, env.OpenRequest{Dir: p.Dir, Name: p.Name, Cloud: p.Cloud, UI: ui})
	return m, env.Target{Dir: p.Dir, Name: p.Name}, err
}

// result turns an outcome into a tool answer. Errors are returned to the
// agent as tool errors with the fix, never as protocol failures.
func result(ui *capture, extra string, err error) (*mcp.CallToolResult, Text, error) {
	log := strings.TrimSpace(ui.buf.String())
	if extra != "" {
		log = strings.TrimSpace(log + "\n" + extra)
	}
	if err != nil {
		e := errs.As(err)
		msg := fmt.Sprintf("%s [%s]", e.Message, e.Code)
		if e.Fix != "" {
			msg += "\nFix: " + e.Fix
		}
		if len(ui.asked) > 0 {
			msg += "\nprotolift asked: " + strings.Join(ui.asked, " / ")
		}
		log = strings.TrimSpace(log + "\n" + msg)
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: log}}}, Text{Log: log}, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: log}}}, Text{Log: log}, nil
}

func (s *Server) status(ctx context.Context, _ *mcp.CallToolRequest, in Project) (*mcp.CallToolResult, Text, error) {
	ui := &capture{}
	m, t, err := s.manager(ctx, in, ui)
	if err != nil {
		return result(ui, "", err)
	}
	r, err := m.Status(ctx, t)
	return result(ui, formatStatus(r), err)
}

func (s *Server) list(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, Text, error) {
	ui := &capture{}
	m, err := s.open(ctx, env.OpenRequest{Dir: ".", Cloud: in.Cloud, Region: in.Region, UI: ui})
	if err != nil {
		return result(ui, "", err)
	}
	boxes, err := m.List(ctx)
	var b strings.Builder
	for _, x := range boxes {
		fmt.Fprintf(&b, "%s · %s · %s\n", x.Name, x.State, x.InstanceType)
	}
	if err == nil && len(boxes) == 0 {
		b.WriteString("no protolift boxes in this region")
	}
	return result(ui, b.String(), err)
}

func (s *Server) cost(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, Text, error) {
	ui := &capture{}
	m, _, err := s.manager(ctx, Project{Dir: "."}, ui)
	if err != nil {
		return result(ui, "", err)
	}
	r, err := m.Cost(ctx)
	return result(ui, formatCost(r), err)
}

func key(p Project) string { return p.Dir + "\x00" + p.Name }

func (s *Server) up(ctx context.Context, _ *mcp.CallToolRequest, in UpInput) (*mcp.CallToolResult, Text, error) {
	ui := &capture{confirm: in.ConfirmCreate}
	m, t, err := s.manager(ctx, in.Project, ui)
	if err != nil {
		return result(ui, "", err)
	}
	if err := m.Up(ctx, env.UpRequest{Target: t, Size: in.Size, Detached: true}); err != nil {
		return result(ui, "", err)
	}
	s.startMirror(in.Project, m, t)
	return result(ui, "ports are mirrored to localhost while this session runs", nil)
}

// startMirror keeps the ports up in the background, one mirror per project.
// It is tied to the server's lifetime, not to the tool call.
func (s *Server) startMirror(p Project, m *env.Manager, t env.Target) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.mirrors[key(p)]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mirrors[key(p)] = cancel
	go func() {
		// The mirror reports through the call's log only while the call
		// runs; afterwards it reconnects quietly until the session ends.
		if err := m.Mirror(ctx, t); err != nil && ctx.Err() == nil {
			s.mu.Lock()
			delete(s.mirrors, key(p))
			s.mu.Unlock()
		}
	}()
}

func (s *Server) stopMirror(p Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.mirrors[key(p)]; ok {
		cancel()
		delete(s.mirrors, key(p))
	}
}

func (s *Server) stopMirrors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, cancel := range s.mirrors {
		cancel()
		delete(s.mirrors, k)
	}
}

func (s *Server) stop(ctx context.Context, _ *mcp.CallToolRequest, in Project) (*mcp.CallToolResult, Text, error) {
	ui := &capture{confirm: true}
	m, t, err := s.manager(ctx, in, ui)
	if err != nil {
		return result(ui, "", err)
	}
	s.stopMirror(in)
	return result(ui, "", m.Stop(ctx, t))
}

func (s *Server) down(ctx context.Context, _ *mcp.CallToolRequest, in ConfirmInput) (*mcp.CallToolResult, Text, error) {
	ui := &capture{confirm: in.Confirm}
	if !in.Confirm {
		return result(ui, "", errs.New(errs.CodeCancelled, "nothing was deleted", "ask the user, then call again with confirm=true"))
	}
	m, t, err := s.manager(ctx, in.Project, ui)
	if err != nil {
		return result(ui, "", err)
	}
	s.stopMirror(in.Project)
	return result(ui, "", m.Down(ctx, t))
}

func (s *Server) setBudget(ctx context.Context, _ *mcp.CallToolRequest, in BudgetInput) (*mcp.CallToolResult, Text, error) {
	ui := &capture{}
	dir := in.Dir
	if dir == "" {
		dir = "."
	}
	m, t, err := s.manager(ctx, Project{Dir: dir, Name: in.Name, Cloud: in.Cloud}, ui)
	if err != nil {
		return result(ui, "", err)
	}
	b := env.Budget{DailyUSD: in.DailyUSD, MonthlyUSD: in.MonthlyUSD}
	if in.All {
		err = m.SetGlobalBudget(b)
	} else {
		err = m.SetBudget(t, b)
	}
	return result(ui, fmt.Sprintf("budget set: daily $%.2f, monthly $%.2f (0 = no cap); it applies from the next up", b.DailyUSD, b.MonthlyUSD), err)
}

func formatStatus(r env.StatusReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project %s\n", r.Project)
	if r.Box == nil {
		b.WriteString("no box yet\n")
	} else {
		fmt.Fprintf(&b, "box %s · %s · %s · %s\n", r.Box.Name, r.Box.State, r.Box.InstanceType, r.Price)
		if r.CostSoFar != "" {
			fmt.Fprintf(&b, "cost %s\n", r.CostSoFar)
		}
		for _, p := range r.Ports {
			fmt.Fprintf(&b, "port http://localhost:%d\n", p)
		}
		for _, x := range r.Sync {
			fmt.Fprintf(&b, "sync %s\n", x)
		}
		if r.Advice != "" {
			fmt.Fprintf(&b, "tip %s\n", r.Advice)
		}
	}
	fmt.Fprintf(&b, "spent today $%.2f, this month $%.2f; caps daily $%.2f, monthly $%.2f (0 = none)\n",
		r.SpentToday, r.SpentMonth, r.Budget.DailyUSD, r.Budget.MonthlyUSD)
	return b.String()
}

func formatCost(r env.CostReport) string {
	var b strings.Builder
	for _, l := range r.Lines {
		fmt.Fprintf(&b, "%s (%s %s): today $%.2f, month $%.2f\n", l.Name, l.Provider, l.Region, l.Today, l.Month)
	}
	fmt.Fprintf(&b, "total: today $%.2f, month $%.2f; total caps daily $%.2f, monthly $%.2f (0 = none)\n%s\n",
		r.Today, r.Month, r.Global.DailyUSD, r.Global.MonthlyUSD, r.Note)
	return b.String()
}
