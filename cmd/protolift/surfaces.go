// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/mcpserver"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
	"github.com/Prabhudas111/protolift/internal/webui"
)

// opener turns an OpenRequest from any surface (MCP, local screen) into a
// wired Manager. It is the composition root's one entry for those surfaces.
func opener(base opts) env.Opener {
	return func(ctx context.Context, r env.OpenRequest) (*env.Manager, error) {
		o := base
		o.dir, o.name = orDefault(r.Dir, "."), r.Name
		o.cloud = orDefault(r.Cloud, base.cloud)
		o.profile = orDefault(r.Profile, base.profile)
		o.project = orDefault(r.Project, base.project)
		o.region = orDefault(r.Region, base.region)
		return wire(ctx, "up", o, r.UI)
	}
}

// runMCP serves the Model Context Protocol on stdin/stdout. Nothing else
// may write to stdout while it runs: stdout is the protocol channel.
func runMCP(ctx context.Context, o opts) error {
	if err := mcpserver.New(opener(o), version).Run(ctx); err != nil && ctx.Err() == nil {
		return errs.Wrap(errs.CodeInternal, err, "the MCP server stopped", "check the client's MCP logs; restart the client")
	}
	return nil
}

// printJSON is --json: stable, machine-readable output for scripts and
// agents that call the CLI directly.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot write JSON", "")
	}
	return nil
}

// runUI serves the local control screen until Ctrl-C.
func runUI(ctx context.Context, o opts, stdout io.Writer) error {
	srv, ln, err := webui.New(opener(o), uiPort(o.port))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "protolift control screen (this computer only):")
	fmt.Fprintln(stdout, "  "+srv.URL())
	fmt.Fprintln(stdout, "Keep this running; press Ctrl-C to close it.")
	if !o.noOpen {
		openBrowser(srv.URL())
	}
	return srv.Serve(ctx, ln)
}

// uiPort is --port, or 0 (any free port).
func uiPort(p int) int {
	if p < 0 || p > 65535 {
		return 0
	}
	return p
}

// openBrowser is a convenience: if no browser opens, the printed link works.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait() // gate-allow: reaps the browser launcher; its exit status is irrelevant
	}
}

// runMCPHTTP serves MCP over HTTP on 127.0.0.1 with a bearer token.
func runMCPHTTP(ctx context.Context, o opts, stdout io.Writer) error {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(uiPort(o.port)))
	if err != nil {
		return errs.Wrap(errs.CodePortBusy, err, "the MCP port is in use", "pick another with --port")
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return errs.Wrap(errs.CodeInternal, err, "cannot make a secret key", "")
	}
	token := hex.EncodeToString(b)
	fmt.Fprintln(stdout, "protolift MCP over HTTP (this computer only):")
	fmt.Fprintln(stdout, "  URL:   http://"+ln.Addr().String()+"/")
	fmt.Fprintln(stdout, "  Token: "+token+"   (send as: Authorization: Bearer <token>)")
	return mcpserver.New(opener(o), version).HTTP(ctx, ln, token)
}
