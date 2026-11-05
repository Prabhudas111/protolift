# 0003: Mirror ports to the same numbers

**Decision.** Each port a service publishes on the box is forwarded to the same port on the laptop's
loopback. Ports are read from Compose's own resolved config on the box. Nothing in the project is rewritten.

**Why.** URLs, CORS rules, OAuth callbacks and `.env` values that say `localhost:<port>` keep working unchanged.
Reading Compose's resolved config avoids re-implementing variables, overrides and profiles.

**Consequences.** UDP and random host ports cannot be mirrored. protolift names them as warnings. Two
services may not claim one port (`CONFLICT`).
