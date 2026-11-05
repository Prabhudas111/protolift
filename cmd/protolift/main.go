// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0

// Command protolift runs your dev stack in your own AWS account and mirrors
// it to localhost. Built by Prabhudas Avanigadda · https://www.proto-engineer.com
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Prabhudas111/protolift/internal/env"
	"github.com/Prabhudas111/protolift/internal/env/adapters/sshremote"
	"github.com/Prabhudas111/protolift/internal/env/adapters/terminal"
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// version is set at release time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `protolift: run your dev stack in your own cloud account (AWS or Google Cloud), use it on localhost.

Usage:
  protolift <command> [flags]

Commands:
  init        set up your cloud account once (safe to run again)
  up          start the box, sync your files, run your stack, mirror its ports
  status      show the box, its cost, sync state and ports
  list        show every protolift box in this region
  stop        stop the box (your files and data stay on its disk)
  resize      change the box size: protolift resize S|M|L|XL
  down        delete the box and everything it made
  uninstall   remove protolift's one-time setup from this cloud account
  config      show (and create) the settings file for this project
  cost        estimated spend today and this month, per project and in total
  budget      set spending caps: protolift budget --daily 5 --monthly 60 [--all]
  mcp         let AI agents use protolift over MCP (stdio; --http for a local URL)
  ui          open the local control screen (this computer only)
  ssh         open a terminal on the box
  logs        follow logs on the box: protolift logs [folder|service]
  version     print the version

Flags (any command):
  --cloud NAME     aws or gcp (default: aws, or what the project used before)
  --profile NAME   AWS profile (default: your AWS default)
  --project ID     Google Cloud project (default: your gcloud project)
  --region NAME    region, e.g. us-east-1 or us-central1 (default: your CLI default)
  --dir PATH       project folder (default: this folder)
  --name NAME      a second env for the same folder (e.g. per agent)
  --yes            answer yes to every question (for scripts)
  --quiet          only show warnings and errors
  --debug          show the full error behind a failure
  --json           machine-readable output for status, list and cost

Flags for ui and mcp:
  --port N         local port (default: any free port)
  --no-open        ui: print the link without opening a browser
  --http           mcp: serve on a local URL with a token instead of stdio

Flags for up:
  --size S|M|L|XL  box size for a new box (default: suggested from your stack)
  --arch arm|intel chip for a new box (default: detected from your images)
  --no-env         do not copy .env files to the box
  --detach         set everything up, then return without mirroring ports

Free and open source (Apache-2.0). Built by Proto.Engineer: https://www.proto-engineer.com
Learn the tech inside protolift, and report problems: https://github.com/Prabhudas111/protolift
`

func main() {
	if name, ok := sshremote.ShimName(os.Args[0]); ok {
		os.Exit(sshremote.RunShim(name, os.Args[1:]))
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// opts are the parsed flags of one command.
type opts struct {
	cloud, profile, project          string
	region, dir, name                string
	yes, quiet, debug, asJSON        bool
	size, arch                       string
	noEnv, detach, all, http, noOpen bool
	daily, monthly                   float64
	port                             int
	args                             []string
}

func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, rest := argv[0], argv[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, "protolift "+version+" · https://www.proto-engineer.com")
		return 0
	case "__proxy":
		return runProxy(rest, stderr)
	}
	o, err := parse(cmd, rest)
	if err != nil {
		return fail(stderr, err, false)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dispatch(ctx, cmd, o, stdout); err != nil {
		return fail(stderr, err, o.debug)
	}
	return 0
}

// parse reads flags anywhere among the arguments.
func parse(cmd string, rest []string) (opts, error) {
	var o opts
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.cloud, "cloud", "", "")
	fs.StringVar(&o.profile, "profile", "", "")
	fs.StringVar(&o.project, "project", "", "")
	fs.StringVar(&o.region, "region", "", "")
	fs.StringVar(&o.dir, "dir", ".", "")
	fs.StringVar(&o.name, "name", "", "")
	fs.BoolVar(&o.yes, "yes", false, "")
	fs.BoolVar(&o.quiet, "quiet", false, "")
	fs.BoolVar(&o.debug, "debug", false, "")
	fs.BoolVar(&o.asJSON, "json", false, "")
	fs.StringVar(&o.size, "size", "", "")
	fs.StringVar(&o.arch, "arch", "", "")
	fs.BoolVar(&o.noEnv, "no-env", false, "")
	fs.BoolVar(&o.detach, "detach", false, "")
	fs.BoolVar(&o.all, "all", false, "")
	fs.Float64Var(&o.daily, "daily", -1, "")
	fs.Float64Var(&o.monthly, "monthly", -1, "")
	fs.IntVar(&o.port, "port", 0, "")
	fs.BoolVar(&o.http, "http", false, "")
	fs.BoolVar(&o.noOpen, "no-open", false, "")
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return o, errs.Wrap(errs.CodeInvalidInput, err, "unknown flag for "+cmd, "run `protolift help` to see the flags")
		}
		rest = fs.Args()
		if len(rest) > 0 {
			o.args = append(o.args, rest[0])
			rest = rest[1:]
		}
	}
	switch strings.ToLower(o.arch) {
	case "", "arm", "intel":
		o.arch = strings.ToLower(o.arch)
	default:
		return o, errs.New(errs.CodeInvalidInput, fmt.Sprintf("--arch %q is not known", o.arch), "use --arch arm or --arch intel")
	}
	return o, nil
}

func dispatch(ctx context.Context, cmd string, o opts, stdout io.Writer) error {
	known := map[string]bool{"init": true, "up": true, "status": true, "list": true, "stop": true, "resize": true,
		"down": true, "uninstall": true, "config": true, "ssh": true, "logs": true,
		"cost": true, "budget": true, "mcp": true, "ui": true}
	if !known[cmd] {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("%q is not a protolift command", cmd), "run `protolift help` to see the commands")
	}
	switch cmd {
	case "mcp":
		if o.http {
			return runMCPHTTP(ctx, o, stdout)
		}
		return runMCP(ctx, o)
	case "ui":
		return runUI(ctx, o, stdout)
	}
	m, err := wire(ctx, cmd, o, terminal.New(o.yes, o.quiet))
	if err != nil {
		return err
	}
	t := env.Target{Dir: o.dir, Name: o.name}
	switch cmd {
	case "init":
		return m.Init(ctx)
	case "up":
		return m.Up(ctx, env.UpRequest{Target: t, Size: o.size, Arch: env.Arch(o.arch), NoEnv: o.noEnv, Detached: o.detach})
	case "status":
		r, err := m.Status(ctx, t)
		if err != nil {
			return err
		}
		if o.asJSON {
			return printJSON(stdout, r)
		}
		printStatus(stdout, r)
		return nil
	case "list":
		boxes, err := m.List(ctx)
		if err != nil {
			return err
		}
		if o.asJSON {
			return printJSON(stdout, boxes)
		}
		printList(stdout, boxes)
		return nil
	case "stop":
		return m.Stop(ctx, t)
	case "resize":
		if len(o.args) != 1 {
			return errs.New(errs.CodeInvalidInput, "resize needs a size", "for example: protolift resize M")
		}
		return m.Resize(ctx, t, o.args[0])
	case "down":
		return m.Down(ctx, t)
	case "uninstall":
		return m.Uninstall(ctx)
	case "config":
		p, err := m.Config(ctx, t)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "settings for this project (outside your repo):")
		fmt.Fprintln(stdout, "  "+p)
		return nil
	case "cost":
		r, err := m.Cost(ctx)
		if err != nil {
			return err
		}
		if o.asJSON {
			return printJSON(stdout, r)
		}
		printCost(stdout, r)
		return nil
	case "budget":
		return setBudget(ctx, m, t, o, stdout)
	case "ssh":
		return m.SSH(ctx, t)
	case "logs":
		svc := ""
		if len(o.args) > 0 {
			svc = o.args[0]
		}
		return m.Logs(ctx, t, svc)
	}
	return nil
}

// fail prints one error the way a person can act on it, and returns its
// exit code.
func fail(stderr io.Writer, err error, debug bool) int {
	e := errs.As(err)
	if e.Code == errs.CodeCancelled {
		if e.Message != "" {
			fmt.Fprintln(stderr, "✗ "+e.Message)
		}
		return errs.ExitCode(e)
	}
	fmt.Fprintln(stderr, "✗ "+e.Message)
	if e.Fix != "" {
		fmt.Fprintln(stderr, "  Fix: "+e.Fix)
	}
	if debug && e.Cause != nil {
		fmt.Fprintf(stderr, "  [%s] %v\n", e.Code, e.Cause)
		for c := errors.Unwrap(e.Cause); c != nil; c = errors.Unwrap(c) {
			fmt.Fprintf(stderr, "    caused by: %v\n", c)
		}
	} else if e.Cause != nil {
		fmt.Fprintln(stderr, "  (run again with --debug for details)")
	}
	return errs.ExitCode(e)
}
