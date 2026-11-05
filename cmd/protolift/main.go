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
	"github.com/Prabhudas111/protolift/internal/shared/errs"
)

// version is set at release time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `protolift: run your dev stack in your own AWS account, use it on localhost.

Usage:
  protolift <command> [flags]

Commands:
  init        set up your AWS account once (safe to run again)
  up          start the box, sync your files, run your stack, mirror its ports
  status      show the box, its cost, sync state and ports
  list        show every protolift box in this region
  stop        stop the box (your files and data stay on its disk)
  resize      change the box size: protolift resize S|M|L|XL
  down        delete the box and everything it made
  uninstall   remove protolift's one-time setup from this AWS account
  config      show (and create) the settings file for this project
  ssh         open a terminal on the box
  logs        follow logs on the box: protolift logs [folder|service]
  version     print the version

Flags (any command):
  --profile NAME   AWS profile to use (default: your AWS default)
  --region NAME    AWS region (default: your AWS default)
  --dir PATH       project folder (default: this folder)
  --name NAME      a second env for the same folder (e.g. per agent)
  --yes            answer yes to every question (for scripts)
  --quiet          only show warnings and errors
  --debug          show the full error behind a failure

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
	profile, region, dir, name string
	yes, quiet, debug          bool
	size, arch                 string
	noEnv, detach              bool
	args                       []string
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
	fs.StringVar(&o.profile, "profile", "", "")
	fs.StringVar(&o.region, "region", "", "")
	fs.StringVar(&o.dir, "dir", ".", "")
	fs.StringVar(&o.name, "name", "", "")
	fs.BoolVar(&o.yes, "yes", false, "")
	fs.BoolVar(&o.quiet, "quiet", false, "")
	fs.BoolVar(&o.debug, "debug", false, "")
	fs.StringVar(&o.size, "size", "", "")
	fs.StringVar(&o.arch, "arch", "", "")
	fs.BoolVar(&o.noEnv, "no-env", false, "")
	fs.BoolVar(&o.detach, "detach", false, "")
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
		"down": true, "uninstall": true, "config": true, "ssh": true, "logs": true}
	if !known[cmd] {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("%q is not a protolift command", cmd), "run `protolift help` to see the commands")
	}
	m, err := wire(ctx, cmd, o)
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
		printStatus(stdout, r)
		return nil
	case "list":
		boxes, err := m.List(ctx)
		if err != nil {
			return err
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
