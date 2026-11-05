// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	open := func(context.Context, env.OpenRequest) (*env.Manager, error) {
		return nil, errs.New(errs.CodeNotSetUp, "not set up in tests", "")
	}
	s, ln, err := New(open, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page("static/index.html", "text/html"))
	mux.HandleFunc("GET /api/state", s.api(s.state))
	mux.HandleFunc("GET /app.js", s.page("static/app.js", "text/javascript"))
	return s, s.guard(mux)
}

func do(h http.Handler, host, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = host
	if token != "" {
		r.Header.Set("X-Protolift-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuardNeedsTokenAndLoopbackHost(t *testing.T) {
	s, h := testServer(t)
	host := s.addr
	if w := do(h, host, "/api/state", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := do(h, host, "/api/state", "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := do(h, "evil.example"+host[strings.LastIndex(host, ":"):], "/api/state", s.token); w.Code != http.StatusForbidden {
		t.Fatalf("a rebinding host must be refused: %d", w.Code)
	}
	w := do(h, host, "/?t="+s.token, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "protolift") {
		t.Fatalf("page: %d", w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp = %q", csp)
	}
}

func TestAPIErrorsCarryTheFix(t *testing.T) {
	s, h := testServer(t)
	w := do(h, s.addr, "/api/state", s.token)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"NOT_SET_UP"`) {
		t.Fatalf("state error: %d %s", w.Code, w.Body.String())
	}
}

func TestListensOnLoopbackOnly(t *testing.T) {
	s, _ := testServer(t)
	if !strings.HasPrefix(s.addr, "127.0.0.1:") || !strings.Contains(s.URL(), "?t="+s.token) || len(s.token) != 48 {
		t.Fatalf("addr=%s url=%s", s.addr, s.URL())
	}
}

func TestAssetsLoadWithoutTokenButDataDoesNot(t *testing.T) {
	s, h := testServer(t)
	if w := do(h, s.addr, "/app.js", ""); w.Code != http.StatusOK {
		t.Fatalf("the page script must load without a token: %d", w.Code)
	}
	if w := do(h, "evil.example:1", "/app.js", ""); w.Code != http.StatusForbidden {
		t.Fatalf("the host check still applies to assets: %d", w.Code)
	}
	if w := do(h, s.addr, "/", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("the page itself needs the token: %d", w.Code)
	}
}
