# LLD: the box

Owner: `adapters/awscloud/box.go`, `adapters/boxscripts`.

## Resources and tags

| Resource | Created by | Tags | Removed by |
|---|---|---|---|
| IAM role + instance profile `protolift-box` (SSM core only) | `init` | `protolift:managed` | `uninstall` |
| Security group `protolift-box`, **no inbound rules** | `init` | `protolift:managed` | `uninstall` |
| VPC, IGW, 3 subnets (only if the region has no default VPC) | `init` | `protolift:managed`, `Name=protolift` | `uninstall` |
| EC2 instance, its volume and its network card | `up` | `protolift:managed`, `protolift:env`, `protolift:project`, `Name` | `down` |
| CloudWatch alarm `protolift-<env>-idle` | `up` | `protolift:managed`, `protolift:env` | `down` |

`uninstall` refuses (`CONFLICT`) while any box exists, so a running box can never lose its access role.

## Instance

- Ubuntu 24.04 from the public SSM parameter `/aws/service/canonical/ubuntu/server/24.04/stable/current/<arch>/hvm/ebs-gp3/ami-id`.
- 60 GB gp3, encrypted, deleted with the instance. IMDSv2 required.
- `InstanceInitiatedShutdownBehavior = stop`: an idle shutdown keeps the disk.
- A public IP for **outbound** traffic only (packages, registries, SSM). The firewall group has no inbound rules.
- The subnet is in a zone that offers the instance type (`DescribeInstanceTypeOfferings`).

## First boot (`files/boot.sh.tmpl`)

1. apt: docker.io, docker-compose-v2, docker-buildx, git, make, build-essential, curl, unzip, jq. Retried 5×.
2. Node.js (the newest LTS) and Go (the newest stable), from their official sites. `PATH` is set in `/etc/profile.d`.
3. inotify limits raised for dev-server file watchers.
4. `/work` and `/etc/protolift` are owned by `ubuntu`. The idle config is written there.
5. The idle script and its systemd timer are installed.
6. `/var/lib/protolift/ready`, or `failed` plus the log, which `up` shows.

## Idle stop (`files/idle.sh`)

Runs every 5 min as root. It reads only `KEY=number` lines from the config and never executes it.
"Active" means more than 64 KB moved by sshd in the last 5 minutes, or an open terminal (`who` shows a pts).
It records peak memory for `status`. It stops the box at `idle_minutes` of inactivity or `max_hours` of uptime.

## Folders

- Compose: `docker compose -f <file> up -d --build --remove-orphans`. Ports come from
  `docker compose config --format json` (variables, overrides and profiles already applied by Compose).
- Run command: a systemd unit `protolift-<env>-<folder>.service` (`User=ubuntu`, login shell, `Restart=on-failure`).
  It is restarted only when the unit file changed.
- Setup runs when the hash of (setup command + lockfiles) changes.
