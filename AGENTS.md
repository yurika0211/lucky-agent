# AGENTS.md

This file gives project-specific operating guidance for agents working in the
LuckyAgent repository. Keep it factual, compact, and aligned with the current
codebase.

## Project Shape

LuckyAgent is a Go agent runtime, not only a chat wrapper. The main binary is
`cmd/la`, with most runtime code under `internal/`.

Primary entry points:

- `lh`: starts the TUI when run in an interactive terminal.
- `lh init`: initializes `${HOME}/.luckyagent`.
- `lh chat [message]`: local one-shot chat or REPL debugging.
- `lh serve`: HTTP API server.
- `lh msg-gateway start`: external chat gateways.
- `lh rag`: RAG index/search/stats commands.
- `lh config`, `lh soul`, `lh dashboard`, `lh tui`, and `lh learn`: runtime
  management surfaces.

The runtime home defaults to `${HOME}/.luckyagent`. Source checkouts may also
contain a local `config.json`, but deployed runtime state belongs under the
configured home directory.

## Current Runtime Model

`internal/agent` is the coordination center. It builds the system prompt,
packs context, routes skills, recalls memory, queries RAG, executes tools,
streams chat events, and owns the loop-level convergence behavior.

Important context behavior:

- Root `AGENTS.md` or `agents.md` is loaded as project context.
- Runtime manual files are loaded from `description/AGENTS.md`,
  `description/agents.md`, or the legacy `LUCKYHARNESS_AGENT_MANUAL.md` paths.
- Recent session history is filtered by intent, but the latest user turn and
  following messages must remain available to avoid old memory overriding the
  current task.
- Retrieved memory is prior evidence, not the current task itself. Prefer the
  latest user message and explicit session history when they conflict.

## Key Packages

- `internal/agent`: agent loop, context planner, system prompt, tool execution,
  memory gate, skill routing, session-aware chat.
- `internal/config`: config loading, defaults, runtime home initialization.
- `internal/session`: persistent conversation sessions.
- `internal/memory`: Obsidian-compatible Markdown memory vault, activation,
  temporal resolution, hygiene.
- `internal/rag`: RAG indexing, SQLite persistence, retrieval, stream indexer.
- `internal/tool`: built-in tools, skill loading, MCP/opencli/web/filesystem
  adapters, cron/autonomy services.
- `internal/server`: HTTP API, SSE chat, WebSocket, health, context, RAG,
  memory, sessions, soul endpoints.
- `internal/gateway`: shared gateway abstractions and runtime state.
- `internal/gateway/telegram`, `qqofficial`, `napcat`, `weixin`,
  `openclawweixin`: platform adapters.
- `internal/cli/lhcmd`: Cobra command definitions and command handlers.
- `UI/GUI` and `UI/TUI`: frontend workspaces. Run Node commands from the
  relevant UI subdirectory, not the repository root.

## HTTP API Surface

`lh serve` registers the API under `/api/v1`.

Common routes include:

- `POST /api/v1/chat` for SSE chat.
- `POST /api/v1/chat/sync` for synchronous chat.
- `GET /api/v1/sessions` and `/api/v1/sessions/`.
- `GET|POST /api/v1/memory`, `GET /api/v1/memory/recall`,
  `GET /api/v1/memory/stats`.
- `POST /api/v1/rag/index`, `POST /api/v1/rag/search`,
  `GET /api/v1/rag/stats`, `/api/v1/rag/store`.
- `/api/v1/rag/stream/*` for stream indexer watch, scan, start, stop, queue,
  status, and process operations.
- `/api/v1/context` and `/api/v1/context/fit` for context inspection.
- `/api/v1/health/live`, `/ready`, `/detail`, and `/api/v1/metrics`.
- `/api/v1/ws` and `/api/v1/ws/stats`.

Check `internal/server/server.go` before documenting or relying on an endpoint.

## Gateways

Gateway startup uses:

```bash
lh msg-gateway start --platform telegram
lh msg-gateway start --platform qqofficial
lh msg-gateway start --platform napcat
lh msg-gateway start --platform weixin
lh msg-gateway start --platform openclawweixin
```

Telegram supports progress-message modes and session commands. NapCat uses
OneBot v11 reverse WebSocket settings (`listen_addr`, `path`, `access_token`).
QQ Official and Weixin have their own auth/config paths. Verify adapter behavior
and tests under the matching `internal/gateway/<platform>` package before
changing platform-specific assumptions.

`/lucky on` and `/lucky off` collect multiple gateway messages into one user
turn through `internal/gateway/collector`, preserving segment boundaries and
attachments.

## Memory, RAG, And Context

Memory is stored in the LuckyAgent Markdown vault under
`${HOME}/.luckyagent/memory`. It is the durable memory source of truth. RAG is
separate retrieved evidence and may use SQLite persistence under the runtime
home.

Do not treat session history, memory, and RAG as interchangeable:

- Sessions carry chat continuity.
- Memory stores durable facts, preferences, project rules, and decisions.
- RAG stores indexed documents and final-answer artifacts when enabled.

When debugging context contamination, inspect:

- `internal/agent/context_planner.go`
- `internal/agent/system_prompt.go`
- `internal/agent/memory_gate.go`
- `internal/session/session.go`
- relevant gateway handler session writes

## Config Notes

Core config lives in `${HOME}/.luckyagent/config.json`.

High-impact keys:

- `provider`, `api_key`, `api_base`, `model`
- `embedding.*`
- `opencli.*`
- `memory.short_term_max_turns`
- `context.max_history_turns`, `context.max_context_tokens`,
  `context.compression_threshold`
- `agent.max_iterations`, `agent.timeout_seconds`, `agent.auto_approve`,
  `agent.context_debug`
- `server.addr`, `server.api_keys`, `server.enable_cors`
- `dashboard.addr`
- `msg_gateway.*`
- `autonomy.*`
- `hooks.*`

Use `lh config get`, `lh config set`, or inspect `config.example.json` before
claiming a key exists.

## Engineering Workflow

Before editing code:

1. Run `git status --short` and note unrelated dirty files.
2. Inspect the smallest relevant code path and nearby tests.
3. Keep patches scoped to the user request.
4. Do not revert user changes or unrelated dirty files.

Use `rg`/`rg --files` for repository search. Use `apply_patch` for manual edits.

Focused test examples:

```bash
go test ./internal/agent
go test ./internal/config
go test ./internal/server
go test ./internal/gateway/telegram
go test ./internal/gateway/napcat
go test ./internal/memory ./internal/rag
```

Large `go test ./...` runs may be noisy or slow; prefer focused packages unless
the change crosses package boundaries. For UI work, run commands inside
`UI/GUI` or `UI/TUI`.

## Response Rules For Agents

- Lead with the outcome.
- Distinguish verified facts from inference.
- Mention exact files, commands, ports, config keys, and test results when they
  matter.
- If verification was skipped or failed, say so plainly.
- Do not claim a command, test, deployment, cleanup, commit, or push succeeded
  without direct evidence.
- Keep explanations operational; avoid broad philosophy or transient debug logs
  in this file.
