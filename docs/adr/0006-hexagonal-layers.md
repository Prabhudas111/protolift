# 0006: Hexagonal layers behind one manager

**Decision.** `domain → application → ports → adapters`, with `env.Manager` as the only API the CLI uses and
`cmd/protolift/wire.go` as the only composition root. `internal/archtest` fails the build when a layer imports
something it may not.

**Why.** A provider change (another cloud, another sync tool) touches one adapter and nothing else. Use cases
are tested with fakes, without AWS. Rules enforced by a test do not erode.

**Consequences.** Shell syntax lives in an adapter (`boxscripts`), not in use cases. Sharing code between
adapters goes through `internal/shared`, never adapter → adapter.
