# 0007: Clouds are plug-ins behind ports.Cloud

**Decision.** Everything cloud-specific (machine types, prices, which types a region offers, the ssh
ProxyCommand, HostName, how the box gets its key, stop times) belongs to the cloud adapter. The core only knows
cloud-neutral sizes (S/M/L/XL as CPU and memory). `cmd/protolift/providers.go` is the registry: name, how to
open, which laptop tool the tunnel needs.

**Why.** Users choose their own cloud. Adding one must not touch the use cases or other adapters.

**Consequences.** AWS uses protolift's own SSM proxy and 60 s keys. Google Cloud uses `gcloud compute
start-iap-tunnel` and a box-only key in instance metadata (project keys blocked, OS Login off). Azure is one
more adapter and one registry line.
