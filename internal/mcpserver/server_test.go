// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

func failingOpen(context.Context, env.OpenRequest) (*env.Manager, error) {
	return nil, errs.New(errs.CodeAuthExpired, "your AWS login has expired", "run `aws sso login`")
}

// connect runs the server in memory and returns a connected client session.
func connect(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.build().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestToolsAndSafetyHints(t *testing.T) {
	cs := connect(t, New(failingOpen, "test"))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	for _, n := range []string{"protolift_status", "protolift_list", "protolift_cost", "protolift_up", "protolift_stop", "protolift_down", "protolift_set_budget"} {
		if byName[n] == nil {
			t.Fatalf("missing tool %s", n)
		}
	}
	if !byName["protolift_cost"].Annotations.ReadOnlyHint || *byName["protolift_down"].Annotations.DestructiveHint != true {
		t.Fatal("read-only and destructive hints must be set")
	}
	if !strings.Contains(byName["protolift_up"].Description, "confirm_create") {
		t.Fatal("up must tell agents to confirm before creating")
	}
}

func TestDownNeedsConfirmAndErrorsCarryFix(t *testing.T) {
	cs := connect(t, New(failingOpen, "test"))
	ctx := context.Background()
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "protolift_down", Arguments: map[string]any{"dir": "/tmp/x", "confirm": false}})
	if err != nil || !r.IsError || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "nothing was deleted") {
		t.Fatalf("down without confirm: %+v %v", r, err)
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "protolift_status", Arguments: map[string]any{"dir": "/tmp/x"}})
	if err != nil || !r.IsError {
		t.Fatalf("status must fail as a tool error, not a protocol error: %+v %v", r, err)
	}
	if txt := r.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "AUTH_EXPIRED") || !strings.Contains(txt, "aws sso login") {
		t.Fatalf("the agent must get the code and the fix: %q", txt)
	}
}
