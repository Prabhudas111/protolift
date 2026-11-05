# LLD: connection, tunnel and sync

Owner: `adapters/sshremote`, `adapters/awscloud/proxy.go`, `adapters/mutagensync`.

## SSH over SSM

```text
ssh -F ~/.protolift/ssh/config protolift-<env>
  └─ ProxyCommand: protolift __proxy --region R [--profile P] --pubkey K.pub <instance-id> 22
       1. ec2-instance-connect:SendSSHPublicKey  (key valid 60 s, user ubuntu)
       2. ssm:StartSession  document AWS-StartSSHSession, portNumber=22
       3. exec session-manager-plugin <session> <region> StartSession <profile> <request> <endpoint>
          stdin/stdout of the plugin = the ssh byte stream
```

- `~/.protolift/ssh/config` holds only `Include envs/*.conf`. Each box has one file, written when it changes.
- `HostKeyAlias = <instance-id>`. A new box is a new alias, so a recreated box never trips a host-key mismatch.
  `Forget` removes the old line on `down`.
- `ControlMaster auto` (not on Windows). The socket goes in `/tmp/protolift-<uid>/cm/%C` (owner-checked), because
  socket paths are limited to 104 bytes (`internal/shared/privdir`).
- After first boot, `Remote.Reset` closes the shared connection. A login made before boot added `ubuntu` to the
  `docker` group cannot use Docker.

## Commands on the box

`Run` sends the script on **stdin** to `bash -s`. The script itself is never quoted into an argument.
Exit 255 means ssh could not connect (`NETWORK`). Any other non-zero exit is the command failing (`REMOTE_FAILED`,
with the last 2000 bytes of stderr). Each call has its own timeout.

`PutFile` writes `path.protolift-tmp`, then `chmod`, then `mv`. The update is atomic, and `umask 077` keeps
the temp file private.

## Port mirror

```text
ssh -N -o ExitOnForwardFailure=yes -o ControlMaster=no -o ControlPath=none \
    -L 3000:127.0.0.1:3000 -L 8080:127.0.0.1:8080 protolift-<env>
```

- It runs on its **own** connection. Through a shared one, ssh hands the forwards to the master and exits,
  and the ports vanish when the master expires. (Found in the live test, see wiring.md.)
- "Connected" means the first local port accepts a TCP connection, which ssh allows only after it has
  authenticated.
- Reconnect backoff runs from 2 s to a 30 s cap. A connection that lived more than 1 minute resets the backoff.
- `CheckFree` binds `127.0.0.1:p` before starting. A busy port is `PORT_BUSY`, with the command that shows its owner.

## File sync

- A pinned Mutagen (`v0.18.1`, SHA-256 per platform), downloaded once into `~/.protolift/tools`.
- It has a private data folder (`MUTAGEN_DATA_DIRECTORY`), and so its own daemon. A user's own Mutagen is never touched.
- Mutagen runs `ssh` and `scp` by name, so `MUTAGEN_SSH_PATH` points at a folder holding copies of protolift
  named `ssh` and `scp`. Under those names the binary adds `-F <protolift config>` and runs the real program.
- Session per folder: `protolift-<env>-<n>`, labels `protolift-env=<env>` and `protolift-sync=v2`. A session
  with another settings version is recreated.
- Mode `two-way-resolved` (the laptop wins conflicts), `--ignore-vcs`, `posix-raw` symlinks. On the box, files
  are `0644` and folders `0755`, so services in containers (other users) can read them.
- Ignored: build output, caches, dependency folders, `.env*` (they travel separately, one way), and the
  project's top-level `.gitignore` (no negations).
