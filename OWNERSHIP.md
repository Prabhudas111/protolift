# Ownership

Every capability has **one** owner. To change a capability, change its owner. If you find
yourself changing two owners for one idea, the boundary is wrong. Fix the boundary instead.

| Capability | Owner | Notes |
|---|---|---|
| Command line, flags, help text, exit | `cmd/protolift/main.go` | The only place that reads `os.Args`. |
| Wiring adapters to use cases | `cmd/protolift/wire.go` | The composition root. Nothing else constructs adapters. |
| Use-case order and policy | `internal/env/application` | One file per group of use cases. |
| Public API of the env context | `internal/env/manager.go` | The CLI depends only on this. |
| Env identity, sizes, prices, chip choice, memory estimate, port rules | `internal/env/domain` | Pure. No I/O. |
| Contracts between layers | `internal/env/ports` | One interface per outside dependency. |
| Which clouds exist (the plug-in registry) | `cmd/protolift/providers.go` | Add a cloud = one adapter + one entry here. |
| Every AWS call, EC2 types and prices, AWS error translation, the SSM proxy | `adapters/awscloud` | Only package importing the AWS SDK. |
| Every Google Cloud call, machine types, gcloud error translation, the IAP tunnel | `adapters/gcpcloud` | Drives the gcloud CLI; one login for API and IAP. |
| Budgets, the running-time ledger, spend rules | `internal/env/domain/cost.go` | Pure; the store keeps the ledger file. |
| MCP server (AI agents) | `internal/mcpserver` | Uses only `env.Manager`. Destructive tools need confirm. |
| Local control screen | `internal/webui` | Uses only `env.Manager`. 127.0.0.1, token, host check, CSP. |
| The Agent Skill shipped to users | `skills/protolift/SKILL.md` | Rules agents follow; keep in step with the MCP tools. |
| SSH config, keys, remote run/copy/attach, port mirror, ssh/scp shim | `adapters/sshremote` | Never edits the user's `~/.ssh`. |
| File sync, the Mutagen download and its checksums | `adapters/mutagensync` | Private data folder and daemon. |
| Laptop state, settings file, compose/Dockerfile reading, `.env` discovery | `adapters/filestore` | Never writes into a project. |
| Which chips an image supports | `adapters/registry` | Unknown is a valid answer. |
| Every bash line that runs on the box | `adapters/boxscripts` | All quoting lives here. |
| Talking to the person at the terminal | `adapters/terminal` | |
| The error model and exit codes | `internal/shared/errs` | |
| Retry and backoff | `internal/shared/retry` | The only retry loop. |
| Where sockets may live | `internal/shared/privdir` | Socket path limits and folder safety. |
| Layer rules and the private-name guard | `internal/archtest` | Fails CI when broken. |
