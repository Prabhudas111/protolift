# 0004: Two-way sync with a pinned Mutagen and a private daemon

**Decision.** File sync uses Mutagen `v0.18.1`, downloaded once and checked against built-in SHA-256 sums. It
runs with a private data folder (its own daemon), reaching the box through an `ssh` shim that adds
protolift's config. Mode `two-way-resolved` (the laptop wins). On the box, files are `0644` and folders `0755`.

**Why.** Mutagen is mature, fast and handles both directions. Pinning plus checksums protects against a
tampered download. The private daemon never interferes with a Mutagen the user already runs. Readable modes let
container services (other users) read synced files: the owner-only default broke a web server in the live test.

**Consequences.** Settings changes bump the `protolift-sync` label version, so old sessions are recreated.
