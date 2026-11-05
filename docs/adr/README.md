# Decisions

Each decision is frozen once accepted. To change one, write a new record that replaces it.

| # | Decision | Status |
|---|---|---|
| [0001](0001-no-server-own-account.md) | No server of ours: the CLI uses the user's own AWS account and login | Accepted 2026-11-05 |
| [0002](0002-ssm-only-access.md) | Access only through AWS Systems Manager. No inbound rules, 60 s keys | Accepted 2026-11-05 |
| [0003](0003-mirror-ports-same-numbers.md) | Mirror every port to the same number on localhost. Never edit the project | Accepted 2026-11-05 |
| [0004](0004-mutagen-private-daemon.md) | Two-way sync with a pinned Mutagen and a private daemon | Accepted 2026-11-05 |
| [0005](0005-three-layer-idle-stop.md) | Three independent ways a box stops. A quiet tunnel is not activity | Accepted 2026-11-05 |
| [0006](0006-hexagonal-layers.md) | Hexagonal layers behind one manager, enforced by a test | Accepted 2026-11-05 |
| [0007](0007-cloud-plugins.md) | Clouds are plug-ins behind ports.Cloud (AWS, Google Cloud) | Accepted 2026-11-05 |
| [0008](0008-cost-control.md) | Budgets enforced on the box, from a laptop ledger | Accepted 2026-11-05 |
| [0009](0009-agent-surfaces.md) | AI agents use MCP, an Agent Skill and JSON output | Accepted 2026-11-05 |
| [0010](0010-local-control-screen.md) | A local control screen, loopback only | Accepted 2026-11-05 |
