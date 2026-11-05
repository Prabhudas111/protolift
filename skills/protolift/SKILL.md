---
name: protolift
description: Run a project's dev stack on a cloud box in the user's own AWS or Google Cloud account and use it on localhost. Use when the user's laptop is too slow or too full to run their services or Docker, or when they ask to start, stop, check cost of, or delete a protolift box.
---

# protolift

protolift moves a dev stack (a compose file, or run commands) to a private cloud box in the
user's **own** AWS or Google Cloud account, syncs files both ways, and mirrors every port to the
same port on `localhost`. The user pays the cloud directly.

## How to use it (in this order of preference)

1. **MCP tools**, if the `protolift` MCP server is connected: `protolift_status`, `protolift_list`,
   `protolift_cost`, `protolift_up`, `protolift_stop`, `protolift_down`, `protolift_set_budget`.
2. **The CLI with `--json`**: `protolift status --json`, `protolift list --json`, `protolift cost --json`.
3. **The CLI**: `protolift up --detach --yes` only after the user agreed to the price.

## Rules you must follow

- **Money.** Creating a box costs money. Call `protolift_up` without `confirm_create` first; it
  returns the size and price. Tell the user the price and get a clear yes before calling again
  with `confirm_create=true`. Never pass `--yes` to the CLI without that yes.
- **Deleting.** `protolift_down` / `protolift down` deletes the box and its disk. Ask first, every time.
- **Prefer stop over down.** When the user is done for now, `stop` keeps their data and stops compute billing.
- **Budgets.** If a call fails with `BUDGET`, do not work around it. Tell the user which cap was
  reached and offer to raise it with `protolift_set_budget` only if they ask.
- **Errors.** Every error has a code and a `Fix:` line. Relay the fix in plain words. Codes:
  `NOT_SET_UP` → run `protolift init` (once per account), `AUTH_EXPIRED` → the user logs in
  (`aws sso login` / `gcloud auth login`), `PORT_BUSY` → a local app holds the port.
- **Secrets.** `.env` files are copied to the box automatically and never printed. Never print them yourself.

## Typical flows

- "My laptop can't run this project" → `protolift_status` (is there a box?) → if none, `protolift_up`
  (price) → user agrees → `protolift_up` with `confirm_create=true` → give the user the
  `http://localhost:<port>` links from the answer.
- "How much am I spending?" → `protolift_cost`.
- "Stop everything" → `protolift_list`, then `protolift_stop` for each running box the user named.

Source and docs: https://github.com/Prabhudas111/protolift · Built by Proto.Engineer (https://www.proto-engineer.com)
