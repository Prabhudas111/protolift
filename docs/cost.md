# Cost control

protolift is built so a forgotten box cannot run up a bill.

## What stops a box

| Stop | Where it runs | When |
|---|---|---|
| Idle | on the box, every 5 min | no tunnel or sync traffic and no open terminal for `idle_minutes` (default 30) |
| Budget | on the box, every 5 min | the minutes the remaining daily/monthly budget pays for, worked out at `up` |
| Hard limit | on the box | `max_hours` of uptime (default 12) |
| Backstop | AWS: CloudWatch alarm · GCP: max run duration | AWS: CPU < 5% for an hour. GCP: `max_hours` enforced by Compute Engine |

The budget, idle and hard-limit stops run on the box itself, so they work with the laptop closed.

## Budgets

```sh
protolift budget --daily 5 --monthly 60          # this project
protolift budget --daily 10 --monthly 120 --all  # every project together
protolift cost                                    # spend today and this month
protolift ui                                      # the same, on a local screen
```

- `up` refuses to start or create a box once a cap is used up (`BUDGET`, exit code 9).
- Otherwise the box is told how many minutes it may run. The tightest cap (project or total, daily or
  monthly) wins.
- Spend is an **estimate**: running time × list price, from a ledger on your laptop
  (`~/.protolift/ledger.json`). Runs are rebuilt from the cloud's own start and stop times, so a box
  that stopped itself while the laptop was off is still counted. Your cloud bill is the real amount.
- Disk storage while stopped is not in the estimate (a 60 GB disk is about $5/month on either cloud).

## Settings

`protolift config` shows the project's settings file (kept outside your repo):

```toml
idle_minutes = 30
max_hours = 12
daily_budget_usd = 5
monthly_budget_usd = 60
```
