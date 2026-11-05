# Using protolift from AI agents

protolift offers four ways in. They share the same rules: anything that costs money or deletes
needs an explicit confirmation, and every error carries a code and a fix.

| Way | Best for | How |
|---|---|---|
| MCP over stdio | Claude Code, Cursor, VS Code, Codex, Gemini CLI, Claude Desktop | `protolift mcp` |
| MCP over HTTP | clients that connect to a URL | `protolift mcp --http --port 8765` (prints URL + token) |
| Agent Skill | any agent that reads `SKILL.md` | [skills/protolift/SKILL.md](../skills/protolift/SKILL.md) |
| CLI + JSON | scripts and terminal agents | `protolift status --json`, `list --json`, `cost --json` |

## Add the MCP server

Claude Code:

```sh
claude mcp add protolift -- protolift mcp
```

Codex:

```sh
codex mcp add protolift -- protolift mcp
```

Cursor (`.cursor/mcp.json`), VS Code (`.vscode/mcp.json`, key `servers`), Claude Desktop and Gemini CLI use
the same shape:

```json
{ "mcpServers": { "protolift": { "command": "protolift", "args": ["mcp"] } } }
```

Pass a cloud or login if it is not your default: `"args": ["mcp", "--cloud", "gcp", "--project", "my-project"]`.

## Tools

| Tool | Changes anything? | Notes |
|---|---|---|
| `protolift_status` | no | box, size, price, cost so far, sync, ports, budget |
| `protolift_list` | no | every box in the cloud and region |
| `protolift_cost` | no | spend today and this month, per project and total |
| `protolift_up` | yes, may cost money | creating a box needs `confirm_create=true`. Ports stay mirrored for the MCP session |
| `protolift_stop` | yes | keeps the disk and stops compute billing |
| `protolift_down` | yes, deletes | needs `confirm=true` |
| `protolift_set_budget` | yes | daily and monthly caps, per project or `all=true` |

## Install the Agent Skill

Copy `skills/protolift` into your agent's skills folder (for Claude Code: `~/.claude/skills/protolift`).
