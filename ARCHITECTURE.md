# protolift architecture (HLD)

## 1. Problem

A developer's laptop cannot run their stack (CPU, memory or disk). They want the same stack in the
cloud, used from the laptop exactly as before: same `localhost` ports, live code changes, no project
edits, and a bill only from their own cloud account (AWS or Google Cloud), kept inside a budget.

## 2. Principles

1. **No server of ours.** The CLI talks straight to AWS with the user's login. Nothing to run, nothing to breach.
2. **Mirror, don't rewrite.** Ports keep their numbers on both sides, so URLs, CORS rules and env values that
   say `localhost` keep working. The project is never edited.
3. **Closed by default.** The box has no inbound rules. All access goes through AWS Systems Manager.
   SSH keys are pushed for 60 seconds per connection and never stored on the box.
4. **Cost-safe by default.** There are three independent ways the box stops (§6), and `down` removes every
   tagged resource.
5. **One owner per job.** Every capability has exactly one package that owns it (see [OWNERSHIP.md](OWNERSHIP.md)).

## 3. System view

```text
 laptop                                           AWS account (the user's)
 ─────────────────────────────────────────        ─────────────────────────────────────────
 protolift CLI ── AWS SDK ─────────────────────▶  EC2 · IAM · SSM · CloudWatch · Instance Connect
     │                                                         │
     ├─ ssh ── ProxyCommand: protolift __proxy ── SSM session ─┼─▶ box sshd (localhost only reachable)
     │          (pushes a 60 s key, then streams)              │
     ├─ ssh -N -L p:127.0.0.1:p  (port mirror) ────────────────┤    docker compose / systemd services
     └─ mutagen (private daemon) ── ssh shim ── same tunnel ───┘    /work/<folder>  (two-way sync)
```

## 4. Layers (hexagonal, inside `internal/env`)

Ways in (driving adapters), all through `env.Manager` only: the CLI (`cmd/protolift`), the MCP server
(`internal/mcpserver`) and the local screen (`internal/webui`). Clouds are plug-ins: `ports.Cloud` owns
everything cloud-specific (types, prices, tunnel command, key delivery), and `cmd/protolift/providers.go` lists
the adapters (`awscloud`, `gcpcloud`).

```text
 cmd/protolift          composition root: flags, wiring, the hidden __proxy and ssh/scp shim
   └─ env.Manager       facade: the only thing the CLI calls
        └─ application  use cases: init, up, status, list, stop, resize, down, uninstall, config, ssh, logs
             ├─ domain  pure rules: ids, sizes, prices, chip choice, memory estimate, compose ports
             └─ ports   interfaces: Cloud, Remote, Tunnel, Sync, Store, ImageInspector, Scripts, UI, Clock
                  └─ adapters (one per port)
                       awscloud   EC2/IAM/SSM/CloudWatch + the SSM proxy
                       sshremote  OpenSSH: run, copy, attach, port mirror, the ssh/scp shim
                       mutagensync pinned, checksum-verified Mutagen with a private daemon
                       filestore  ~/.protolift state, settings, compose/Dockerfile reading
                       registry   OCI registry API, which chips an image supports
                       boxscripts every bash line that runs on the box, quoted safely
                       terminal   the person at the keyboard
 internal/shared        errs (one error model), retry (one backoff), privdir (socket folders)
```

The import rules are enforced by `internal/archtest`. A change that breaks a layer fails the tests.

## 5. `up`, step by step

1. Load the project and settings (outside the repo). Validate folders and ports.
2. `Cloud.Ready`: has `init` been run here? If not: `NOT_SET_UP` with the fix.
3. Find the box by tag `protolift:env=<id>`. Wake it if stopped. If there is none:
   - pick a chip: registry checks, ARM only if every image has it;
   - pick a size: memory estimate;
   - **ask with the price**, then create it.
4. Wait for SSM, then for the first-boot marker. Close any shared ssh connection so the new docker group applies.
5. Write the idle settings to the box.
6. Sync: one Mutagen session per folder, two-way, laptop wins conflicts. Wait for a full flush.
7. Copy `.env` files laptop → box (0600), only when changed.
8. Per folder: setup when the lockfile hash changed, then start (compose `up -d --build`, or a systemd unit).
9. Read ports from Compose's own resolved config on the box. Merge them and refuse duplicates.
10. Check the laptop ports are free. Save state. Print the links.
11. Mirror ports on a dedicated ssh connection, reconnecting with capped backoff. Push `.env` changes until Ctrl-C.

## 6. Cost safety

| Layer | Where | Trigger |
|---|---|---|
| Idle timer | box, every 5 min | no tunnel/sync traffic (sshd bytes) and no open terminal for `idle_minutes` (30) |
| Hard cap | box | uptime ≥ `max_hours` (12) |
| Backstop | CloudWatch alarm → EC2 stop | CPU < 5% for 60 min. Needs neither the box script nor the laptop. |

A connected-but-quiet tunnel does **not** count as activity. That is deliberate: a laptop left open overnight
must not keep a box running.

## 7. Failure handling

- **One error model** (`internal/shared/errs`): every visible failure has a stable code, a plain message and a
  fix. Exit codes come from the code, so scripts can tell failures apart.
- **Every network call is bounded.** AWS calls have a 30 s HTTP timeout and capped SDK retries. Each step of `up` has
  its own limit, and a timeout names the step. Downloads are bounded and checksum-verified.
- **No silent retries.** The one retry helper has attempts, backoff and jitter. The tunnel reconnects
  with a visible message.
- **Partial progress is safe.** State is saved as soon as a box exists, so `down` can always find it.

## 8. Security

- No inbound rules. IMDSv2 required. Encrypted disk. Least-privilege role (`AmazonSSMManagedInstanceCore` only).
- The user's `~/.ssh/config` is never touched. protolift passes its own config with `-F`, also through the
  Mutagen shim.
- Every user value that reaches bash or systemd is quoted by one function, with tests that try injection.
- Socket folders are owner-only and ownership-checked before use.
- Downloads (Mutagen) are pinned by SHA-256. A mismatch is refused.
- CI fails if a private name, real account id or real instance id appears in the repository.

## 9. Cost control

A ledger on the laptop records each run (start, stop, hourly price), rebuilt from the cloud's own start and
stop times. Budgets (project and total, daily and monthly) are checked before anything starts. The box is told
how many minutes its budget pays for and stops itself, so the cap holds with the laptop closed. See docs/cost.md.

## 10. Out of scope for v0.1

Azure (parked: one adapter plus one registry entry), Kubernetes, spot boxes, team sharing.
