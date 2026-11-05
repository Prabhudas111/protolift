# Changelog

## v0.1.0 (2026-11-05)

First release.

- `init`, `up`, `status`, `list`, `stop`, `resize`, `down`, `uninstall`, `config`, `ssh`, `logs`.
- Boxes in your own AWS account, with no inbound rules: access goes through AWS Systems Manager with 60-second SSH keys.
- Two-way live file sync (a pinned, checksum-verified Mutagen with a private daemon).
- Every published port mirrored to the same port on `localhost`, with automatic reconnects.
- Chip chosen from your images (ARM when every image supports it). Size suggested from your stack, with the price shown first.
- Three-layer idle stop: a box timer, a 12-hour cap, and a CloudWatch alarm.
- `.env` files copied laptop → box only, owner-only, never printed.
- Several folders on one box via a settings file kept outside your repo.
- macOS, Linux and Windows on Intel and ARM.

Measured on a fresh box: first `up` in 93 s, later `up` in 15 s, waking a stopped box in 27 s,
and a saved change visible on `localhost` in 0.22 s (median of 10).
