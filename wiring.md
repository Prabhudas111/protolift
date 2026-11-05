# wiring.md

Append-only change log for every code fix. Read top-to-bottom before any new fix in the same area.

Format: newest entries at top. Never delete entries — if superseded, add a new entry referencing the old one.

---

## [2026-11-06 00:30] plain sentences everywhere; licence appendix filled

**Symptom:** The founder flagged semicolons in docs and messages as reading machine-written, and the LICENSE appendix showing "Copyright [yyyy] [name of copyright owner]".
**Root cause:** Prose (docs, error messages and fixes, comments, MCP schema text) joined clauses with semicolons, and the Apache appendix template kept its placeholders.
**Fix:** Every prose semicolon is now two plain sentences (120 lines, 44 files). Code, shell and CSP strings were left alone and reviewed line by line. The appendix reads "Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)". A new archtest `TestDocsUsePlainSentences` fails on any semicolon-joined prose in markdown outside code. It was proven on a planted line.
**Files touched:** docs and markdown at the root, user-facing strings in cmd/ and internal/, LICENSE:189, internal/archtest/arch_test.go
**Wiring mapped:** message text only. Tests asserting messages were re-run (all green).
**Blast radius:** low (text only).
**Tests run:** go vet clean · go test ./... green · the guard catches the planted line.
**Regression risks:** write two sentences, not one with a semicolon. The guard covers markdown. Keep messages in the same style.

---

## [2026-11-05 23:30] GCP live-tested. List filter and wording fixed

**Symptom:** GCP live test in a real project: the second `up` tried to create the box again ("already exists"), and messages said "AWS" on Google Cloud.
**Root cause:** `gcpcloud.ListBoxes` filtered `zone~^<region>-`, but an instance's `zone` is a full URL (…/zones/asia-south1-a), so it never matched and FindBox saw no box. Machine-type listings return plain zone names, which is why Supports worked. Init/Down/Uninstall messages had AWS words in the use cases.
**Fix:** `listFilter` matches `/zones/<region>-` inside the URL (tested). Messages use `Cloud.Provider()`. GCP prices come from the Cloud Billing Catalog (33,567 SKUs, vCPU+RAM per region).
**Files touched:** internal/env/adapters/gcpcloud/{box.go,catalog.go,gcp_test.go}, internal/env/application/lifecycle.go
**Wiring mapped:** `listFilter` ← ListBoxes ← FindBox/Uninstall (gcpcloud only). Messages are only in application/lifecycle.go.
**Blast radius:** low (one adapter, three strings).
**Tests run:** go test ./... green · GCP live (project with an existing default network, asia-south1): init 22 s. Up 278 s on n2-standard-2 with ARM → Intel fallback (no T2A in asia-south1). Mirror through IAP, edit → visible median 0.22 s (10/10). VM has no service account, max run 12 h → STOP, project ssh keys blocked. Firewall tcp:22 from 35.235.240.0/20 only. Stop 53 s, wake 41 s. Down 55 s. Uninstall left 0 instances, disks and firewall rules.
**Regression risks:** any new gcloud `--filter` on instances must treat `zone` as a URL. A new region needs Billing Catalog prices, or the budget stop is off there (protolift warns).

---

## [2026-11-05 22:00] multi-cloud, cost, MCP, local screen — v0.1.0 scope

**Symptom:** v0.1 was AWS-only with no spend caps and no way for AI agents or a screen to drive it. The founder froze v0.1 scope as: AWS+GCP plug-ins, MCP, cost control, local UI, skill, no outside collaborators.
**Root cause:** Cloud knowledge leaked outside the cloud adapter: sshremote built the AWS `__proxy` line, and domain sizes held EC2 types and prices. A second cloud could not plug in at one place. Spend had no record and no stop.
**Fix:** `ports.Cloud` owns types, prices, Supports, ProxyCommand, HostName and key delivery. `cmd/protolift/providers.go` is the cloud registry. New `gcpcloud` adapter (gcloud CLI, IAP-only ssh, no service account, max-run-duration backstop). The domain ledger + budgets are checked before start, and BUDGET_MINUTES is enforced by the box idle script. `internal/mcpserver` (stdio + loopback HTTP with token) and `internal/webui` (loopback, token, host check, CSP) use only the `env.Manager` facade through an `env.Opener`. Agent Skill in `skills/protolift`. CONTRIBUTING + workflow close outside PRs.
**Files touched:** internal/env/{domain,ports,application}, adapters/{awscloud,gcpcloud,sshremote,filestore,boxscripts}, internal/{mcpserver,webui,archtest,shared/errs}, cmd/protolift/*, docs/*, skills/*, .github/workflows/close-outside-prs.yml
**Wiring mapped (callers/consumers of touched symbols):**
- `ports.Cloud` → implemented by awscloud, gcpcloud and the application fakes. Used by application/* and passed to sshremote as its Router (wire.go).
- `sshremote.Options` → built only in `cmd/protolift/wire.go`.
- `env.Opener` → built only by `cmd/protolift/surfaces.go`. Consumed by mcpserver and webui.
- `Scripts.IdleConfig(settings, budgetMinutes)` → callers: application/up.go, boxscripts.Boot.
**Blast radius:** high (shared ports and facade), approved as the frozen v0.1 scope.
**Tests run:** go vet clean · go test ./... all green (new: GCP adapter, AWS catalog, ledger/budget, Intel fallback, budget refusal + box minutes, MCP in-memory client, webui guard) · six-platform build · MCP stdio handshake against the real binary (7 tools, confirm enforced) · webui live: 401 without token, 403 on a rebinding Host, CSP set.
**Regression risks (for next fix in this area):**
- New cloud = adapter + providers.go entry only. If a use case needs an `if provider ==` branch, the Cloud port is missing a method.
- archtest `matches`: no trailing slash = exact package. Keep entry points (mcpserver/webui) on the facade only.
- The budget stop runs at 5-minute granularity. Unknown price means no budget stop, and protolift warns. GCP price table is empty until filled from the Billing Catalog.
- MCP stdout is the protocol channel. Nothing may print to stdout in `mcp` mode.
**Live re-test (AWS, test account):** fresh up 111 s. $1/day budget became BUDGET_MINUTES=153 on the box. `cost`, `status --json` correct. A used-up budget refused `up` with the fix. Stop through the screen API stopped the box. Down + uninstall left 0 instances, volumes, SGs, alarms, role.
**Bug the screenshot found:** the guard required the token on app.js/app.css, which the browser fetches without it, so the page loaded unstyled and inert. Public assets (no data) are now exempt from the token, the host check still applies, and a test guards it.
**Related:** ADR 0007–0010, docs/cost.md, docs/agents.md, docs/iam/gcp.md.

---

## [2026-11-05 17:30] protolift v0.1.0 first build — initial

**Symptom:** Developers whose laptops cannot run their stack had only a hand-built cloud box, with copy-plus-rebuild per change (~90 s), one hand-made tunnel per port, and no start, stop or clean-up.
**Root cause:** No single owner existed for the lifecycle (box, private tunnel, sync, ports, idle stop), so each step was done by hand, one-way, or not at all.
**Fix:** The protolift CLI owns the whole lifecycle behind hexagonal layers (`internal/env`: domain → application → ports → adapters, with the `env.Manager` facade). `cmd/protolift/wire.go` is the only composition root.
**Files touched:** everything (new repo). See OWNERSHIP.md for which package owns what.
**Wiring mapped (callers/consumers of touched symbols):**
- `cmd/protolift/main.go` → only `env.Manager` (plus `sshremote.ShimName/RunShim` for the shim, and `awscloud.Proxy` for `__proxy` via wire.go)
- `application.Service` → only `ports.*` interfaces. Fakes in `application/fakes_test.go` implement every port.
- grep: `rg "internal/env/adapters" --type go` → imported only from `cmd/protolift/wire.go` (enforced by `internal/archtest`)
**Blast radius:** new repo. No existing consumers.
**Tests run:** `go vet ./...` clean · `go test ./...` 47 passed, 0 failed · builds for darwin/linux/windows × amd64/arm64 · live end-to-end in a test AWS account: init, up (fresh box 93 s, existing 15 s, wake 27 s), port mirror, edit→visible 0.22 s median (10/10), `.env` 0600 and one-way, status, stop, down, uninstall, then a scan found zero leftovers.
**Bugs the live test found (each fixed at its root):**
- Socket paths over 104 bytes (ssh ControlPath, Mutagen daemon) under a long home → one owner for socket placement, `internal/shared/privdir`, used by both adapters.
- `ssh -L` on a shared connection hands forwards to the master and exits → the port mirror always uses its own connection (`ControlMaster=no`, `ControlPath=none`) in `sshremote/tunnel.go`.
- Mutagen's owner-only default modes hid files from container users (nginx 403) → sessions are created with `--default-file-mode-beta 0644 --default-directory-mode-beta 0755`, plus a `protolift-sync=v2` label so old sessions are recreated.
- A shared connection opened before first boot added `ubuntu` to the docker group could not use Docker → `Remote.Reset` after every boot-ready check (`application/up.go` waitBooted).
- AWS Organizations SCP denials looked like missing IAM permissions → `awscloud.translate` names the org policy and says to pick another `--size`/`--region`.
**Regression risks (for next fix in this area):**
- Never route the port mirror through ControlMaster. `Run`/`PutFile` may share. `Mirror` must not.
- Any new unix socket must go through `privdir.Fits`/`privdir.Short`.
- Changing Mutagen session flags means bumping `configLabel` in `mutagensync/sync.go`. Otherwise existing sessions keep the old flags.
- Every user value reaching bash or systemd goes through `boxscripts.quote` / `systemdQuote`. `TestQuoteSurvivesShell` guards it.
- `internal/archtest` holds only fingerprints of private names. Never add the plain words anywhere in this repo.
**Related:** docs/adr/0001–0006, ARCHITECTURE.md, docs/lld/.

---
