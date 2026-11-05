# protolift

**Run your whole dev stack in your own AWS account, and use it on `localhost` as if it ran on your laptop.**

Your laptop is too slow or too full for Docker and your services? protolift starts a cloud box in
**your** AWS account, keeps your files in sync both ways, runs your stack there, and mirrors every
port back to the same port on your laptop. Your editor, browser and tools do not change.

```text
  your laptop                                   your AWS account
 ┌──────────────────────┐   private tunnel    ┌───────────────────────────┐
 │ editor  ── files ────┼─── live sync ───────┼─▶ /work/your-app          │
 │ browser ── :3000 ────┼─── same ports ──────┼─▶ web :3000  api :8080    │
 │ protolift            │   (no open ports)   │   postgres, redis, ...    │
 └──────────────────────┘                     │   stops itself when idle  │
                                              └───────────────────────────┘
```

- **Your account, your bill.** protolift runs no server. It uses your own AWS login and you pay AWS directly.
- **No project changes.** It reads your existing `compose.yaml` and starts your services with the same port numbers.
- **Live sync.** A save on the laptop reaches the box in well under a second. In our test, a page change was
  visible on `localhost` 0.22 s after saving.
- **Safe on cost.** The box stops itself after 30 idle minutes (and always after 12 hours). An AWS alarm stops
  it after an idle hour even if your laptop is off. `protolift down` deletes everything it made.
- **Private.** The box has no inbound network rules. Access goes only through AWS Systems Manager, with a
  single-use SSH key per connection.
- **Any OS.** macOS, Linux and Windows. One program, no Docker on the laptop.

## Install

macOS / Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/Prabhudas111/protolift/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/Prabhudas111/protolift/main/install.ps1 | iex
```

Or with Go: `go install github.com/Prabhudas111/protolift/cmd/protolift@latest`

You also need, once:

- an AWS login (`aws configure sso` or `aws configure`),
- the [AWS Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
  (`brew install --cask session-manager-plugin` on macOS),
- the OpenSSH client (built into macOS, Linux and Windows 10+).

## Use

```sh
protolift init      # once per AWS account and region (safe to run again)
cd my-app
protolift up        # create or wake the box, sync, start, mirror ports
```

`up` prints your links (`http://localhost:3000` and so on) and keeps the ports mirrored until you press
Ctrl-C. The box keeps running after that and stops itself when idle.

| Command | What it does |
|---|---|
| `protolift init` | Sets up your AWS account once: an access role and a firewall group with no inbound rules. |
| `protolift up` | Starts or wakes the box, syncs files, runs your stack, mirrors its ports. |
| `protolift status` | Box, size, cost so far, sync state, ports, and a size tip from real memory use. |
| `protolift list` | Every protolift box in this region. |
| `protolift stop` | Stops the box. Files and data stay on its disk. |
| `protolift resize S\|M\|L\|XL` | Changes the box size, keeping the disk. |
| `protolift down` | Deletes the box, its disk and its alarm. Nothing is left in AWS. |
| `protolift uninstall` | Removes the one-time setup from `init`. |
| `protolift config` | Shows (and creates) this project's settings file, kept **outside** your repo. |
| `protolift ssh` | Opens a terminal on the box. |
| `protolift logs [service]` | Follows logs on the box. |

### Box sizes

protolift suggests a size from your stack and asks before creating anything. Example prices (On-Demand
Linux, ARM, `us-east-1`, November 2026; AWS bills the real amount):

| Size | CPU | Memory | ARM type | Per hour |
|---|---|---|---|---|
| S | 2 | 8 GB | m7g.large | about $0.08 |
| M | 4 | 16 GB | m7g.xlarge | about $0.16 |
| L | 8 | 32 GB | m7g.2xlarge | about $0.33 |
| XL | 16 | 32 GB | c7g.4xlarge | about $0.58 |

ARM is chosen when every image in your stack supports it, because it costs less. Otherwise Intel is used.

### Stacks without a compose file

Run `protolift config` and describe each folder. Folders can live side by side, for example an API and a web app:

```toml
[[folder]]
path = "."
setup = "npm ci"
run = "npm run dev"
ports = [3000]

[[folder]]
path = "../api"
setup = "go mod download"
run = "make dev"
ports = [8080]
```

### Your `.env` files

`.env` files at the top of each folder are copied **laptop → box only**, owner-only, and their contents are
never printed. Changes are copied while `up` runs. Use `--no-env` to skip them.

## How it works, and the tech inside

protolift is built on AWS Systems Manager tunnels, OpenSSH, [Mutagen](https://github.com/mutagen-io/mutagen)
file sync and Docker Compose. Read [ARCHITECTURE.md](ARCHITECTURE.md) for the design, and
[docs/](docs/) for the details.

Want to learn how pieces like these work: tunnels, networking, cloud, system design? That is what
**[Proto.Engineer](https://www.proto-engineer.com)** teaches.

## AWS permissions

- [docs/iam/init-admin.json](docs/iam/init-admin.json): what `init` and `uninstall` need (one time, usually an admin).
- [docs/iam/daily-user.json](docs/iam/daily-user.json): what `up`, `stop`, `down` and the rest need every day.

If your AWS organization limits box types or regions (a service control policy), protolift says so. Ask your
admin which types are allowed and pick a matching `--size`.

## Troubleshooting

See [docs/troubleshooting.md](docs/troubleshooting.md). Add `--debug` to any command for the full error.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
The names "protolift" and "Proto.Engineer" are not licensed. See [TRADEMARKS.md](TRADEMARKS.md).

Built and maintained by **Prabhudas Avanigadda** · [Proto.Engineer](https://www.proto-engineer.com)
