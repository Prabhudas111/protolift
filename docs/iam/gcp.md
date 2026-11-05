# Google Cloud permissions

protolift uses your own `gcloud` login. Grant these roles on the project:

| For | Roles |
|---|---|
| `protolift init` / `uninstall` (once, usually an admin) | `roles/serviceusage.serviceUsageAdmin` (turn on Compute Engine and IAP), `roles/compute.securityAdmin` (the firewall rule), `roles/compute.networkAdmin` (only if the project has no default network) |
| everyday use (`up`, `stop`, `down`, ...) | `roles/compute.instanceAdmin.v1`, `roles/iap.tunnelResourceAccessor` |

What `init` creates:

- the firewall rule `protolift-allow-iap-ssh`: TCP 22 **only** from Google's IAP range `35.235.240.0/20`,
  only to VMs tagged `protolift`
- a `protolift` network, only if the project has no `default` network

Boxes run with **no service account** and no API scopes, OS Login off, and project-wide SSH keys
blocked. Only the box's own key works, and only through IAP.
