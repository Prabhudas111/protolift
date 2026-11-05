# 0009: AI agents use MCP, an Agent Skill and JSON output

**Decision.** Agents get the official-SDK MCP server (stdio, plus streamable HTTP on 127.0.0.1 with a bearer
token), an Agent Skill (`skills/protolift/SKILL.md`) and `--json` on read commands. Tools that bill or delete
need an explicit confirm field. Errors return as tool errors with code and fix. A2A is not used, since there is no
agent-to-agent need.

**Why.** MCP is the common tool protocol across agent clients, Skills teach the safe procedure, and a JSON CLI
serves terminal agents and scripts. Together they are the most flexible set.
