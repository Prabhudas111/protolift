# wiring.md

Append-only change log for every code fix. Read top-to-bottom before any new fix in the same area.

Format: newest entries at top. Never delete entries — if superseded, add a new entry referencing the old one.

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
**Blast radius:** new repo; no existing consumers.
**Tests run:** `go vet ./...` clean · `go test ./...` 47 passed, 0 failed · builds for darwin/linux/windows × amd64/arm64 · live end-to-end in a test AWS account: init, up (fresh box 93 s, existing 15 s, wake 27 s), port mirror, edit→visible 0.22 s median (10/10), `.env` 0600 and one-way, status, stop, down, uninstall, then a scan found zero leftovers.
**Bugs the live test found (each fixed at its root):**
- Socket paths over 104 bytes (ssh ControlPath, Mutagen daemon) under a long home → one owner for socket placement, `internal/shared/privdir`, used by both adapters.
- `ssh -L` on a shared connection hands forwards to the master and exits → the port mirror always uses its own connection (`ControlMaster=no`, `ControlPath=none`) in `sshremote/tunnel.go`.
- Mutagen's owner-only default modes hid files from container users (nginx 403) → sessions are created with `--default-file-mode-beta 0644 --default-directory-mode-beta 0755`, plus a `protolift-sync=v2` label so old sessions are recreated.
- A shared connection opened before first boot added `ubuntu` to the docker group could not use Docker → `Remote.Reset` after every boot-ready check (`application/up.go` waitBooted).
- AWS Organizations SCP denials looked like missing IAM permissions → `awscloud.translate` names the org policy and says to pick another `--size`/`--region`.
**Regression risks (for next fix in this area):**
- Never route the port mirror through ControlMaster. `Run`/`PutFile` may share; `Mirror` must not.
- Any new unix socket must go through `privdir.Fits`/`privdir.Short`.
- Changing Mutagen session flags means bumping `configLabel` in `mutagensync/sync.go`. Otherwise existing sessions keep the old flags.
- Every user value reaching bash or systemd goes through `boxscripts.quote` / `systemdQuote`; `TestQuoteSurvivesShell` guards it.
- `internal/archtest` holds only fingerprints of private names. Never add the plain words anywhere in this repo.
**Related:** docs/adr/0001–0006, ARCHITECTURE.md, docs/lld/.

---
