# 0001: No server of ours

**Decision.** protolift is a single CLI that calls AWS with the user's own credentials (env, profile or SSO).
There is no protolift service, account or database.

**Why.** Users pay AWS directly and need to trust no third party with their code or keys. Running it costs us
nothing, which keeps a free open-source tool sustainable.

**Consequences.** Every feature must work from the laptop alone. Team features, if they come, are an
optional layer on top and never a requirement.
