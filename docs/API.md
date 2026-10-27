# LuckyAgent HTTP API

The published reference is [the GitHub Pages API guide](https://yurika0211.github.io/lucky-agent/api.html).
This file keeps the field-level examples for the same routes.

This document describes the HTTP API registered by `lh serve`.

Source of truth: `luckyagent-api/routes.go` for route registration,
`luckyagent-app/apiapp/server.go` for application bindings, and its handler files.
If behavior differs from this document, verify the matching
handler before relying on it. Wire contracts and HTTP policy live in
`luckyagent-api`; the main repository retains this product guide.

## Base URL

Default server address:

```text
http://127.0.0.1:9090
```

All application API routes are under:

```text
/api/v1
```

Start the server:

```bash
lh serve
```

or from source:

```bash
go run ./cmd/la serve
```

The root endpoint returns a compact route summary:

```bash
curl http://127.0.0.1:9090/
```

## Authentication

If `server.api_keys` is configured, requests must include one of:

```http
X-API-Key: <key>
```

or:

```http
Authorization: Bearer <key>
```

Query-string API keys are not accepted.

If `server.api_keys` is empty, keyless access requires a loopback connection and
loopback Host, an absent or same-origin Origin, and no forwarding headers.
External reverse proxies must configure keys, including proxies running on the
same machine. The `/health/live` and `/health/ready` probes and root service
index are public for GET/HEAD; health detail follows ordinary authentication.

A key issued by `lh qr` is also accepted. It lives in API process memory,
defaults to 24 hours, and expires when the API restarts. It is not written to
`config.json`.

`server.rate_limit` is the business request quota per credential per minute;
devices sharing one key share that quota. Authentication attempts have a
separate per-peer admission limit, and public health probes have their own
quota. Forwarding headers do not select the client identity. A 429 response
includes `Retry-After: 60`.

JSON bodies default to 2 MiB and must contain one complete JSON value. Multipart
upload endpoints keep their separate limits. Unsupported explicit Content-Type
returns 415; legacy clients omitting the header remain supported.

## Common Response Shape

Most successful responses are JSON. Error responses use:

```json
{
  "error": "message",
  "code": 400,
  "error_code": "invalid_request",
  "request_id": "server-generated-id",
  "details": "optional details"
}
```

Common status codes:

| Code | Meaning |
| --- | --- |
| `200` | OK |
| `201` | Created |
| `400` | Bad request |
| `401` | Missing API key |
| `403` | Invalid API key |
| `404` | Not found |
| `405` | Method not allowed |
| `409` | Conflict |
| `413` | Request body too large |
| `415` | Unsupported media type |
| `429` | Rate limit exceeded |
| `500` | Internal error |
| `503` | Service not initialized |

`X-Request-ID` identifies the response in logs and errors. Existing numeric
`code`, `error` and optional `details` fields remain available.

## Models

### `GET /api/v1/models/effort`

Returns the active chat model's reasoning effort setting and the stable option
list (`auto`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`).
`auto` omits the upstream parameter and uses the model service default.
`supported` is false when the active provider/protocol is not an OpenAI or
OpenAI-compatible endpoint.

### `PUT /api/v1/models/effort`

Persists and hot-applies the setting for subsequent requests. Body:

```json
{"effort":"high"}
```

Explicit values are rejected when the active provider does not support the
OpenAI reasoning parameter. Model-dependent availability is still enforced by
the upstream model service.

## Chat

### `POST /api/v1/chat`

Streaming chat over Server-Sent Events.

An omitted session ID creates a new session. An explicitly supplied session
which does not exist returns 404 before any model or tool execution.
`max_iterations` accepts 0 through 300; zero selects the configured default.

Request body:

```json
{
  "message": "hello",
  "session_id": "optional-session-id",
  "stream": true,
  "max_iterations": 8,
  "auto_approve": false,
  "metadata": {
    "source": "http"
  },
  "attachments": []
}
```

Example:

```bash
curl -N http://127.0.0.1:9090/api/v1/chat \
  -H "Content-Type: application/json" \
  -d "{\"message\":\"hello\"}"
```

SSE frames retain their event type and content and include `run_id`, `event_id`
and an `id:` line. Available creation time and token usage are also forwarded.
A failed event carries a string `error`, `error_code` and `status: "failed"`;
it is not followed by a successful `complete`. Explicit cancellation has
`type: "cancelled"` and `status: "cancelled"` when the connection can still be
written. Only a completed Agent turn ends with `complete,status=completed`.
An incomplete stream is an error. Heartbeats and disconnect cancellation remain
enabled.

SSE event IDs identify the current response; this endpoint does not replay a
persisted SSE cursor. Reposting can execute a new turn. Use the WebSocket
request-ID and reconnect contract for durable retryable delivery.

### `POST /api/v1/chat/sync`

Synchronous chat response.

Unknown explicit sessions also return 404. Successful responses use the actual
session where the messages were saved; there is no sessionless fallback.

Request body is the same as `/api/v1/chat`.

Response includes:

```json
{
  "response": "assistant response",
  "session_id": "session-id",
  "iterations": 1,
  "tokens_used": 0,
  "tool_calls": [],
  "duration": "1.2s"
}
```

Example:

```bash
curl http://127.0.0.1:9090/api/v1/chat/sync \
  -H "Content-Type: application/json" \
  -d "{\"message\":\"hello\"}"
```

An installed plugin command such as `/starter:review UI/HTTP-API.go --Keep-Case`
runs through the normal Agent Loop. The runtime recognizes its exact namespaced
name before the built-in slash-command router, expands the command prompt's
`$ARGUMENTS`, and keeps the argument spelling, case, and hyphens. Expansion is a
prompt operation; it does not evaluate shell substitutions. The chat request
retains its session and attachments, and returns the usual `ChatResponse` above.
The same request fields, including `max_iterations` and `auto_approve`, apply.

```bash
curl http://127.0.0.1:9090/api/v1/chat/sync \
  -H "Content-Type: application/json" \
  -d '{"message":"/starter:review UI/HTTP-API.go --Keep-Case","session_id":"session-id"}'
```

## Commands

These routes use the normal API authentication middleware. The command catalog
includes built-in commands and commands from enabled plugins loaded when the
Agent starts. Install or change plugins with `lh plugin`, then restart the
running API process to load the new installation state. See
[the plugin module guide](plugin-module.md).

### `GET /api/v1/commands`

Returns `{"commands":[...],"count":N}`. Each entry has `name`, `usage`,
`description`, and `group`. Plugin entries use `group: "plugins"` and the exact
`plugin-name:command-name` identifier. For example, the installed starter plugin
adds this entry:

```json
{
  "name": "starter:review",
  "usage": "/starter:review [arguments]",
  "description": "Run installed plugin command",
  "group": "plugins"
}
```

Clients should use the returned name verbatim. Plugin command names retain their
case and hyphens; the built-in command normalization is applied only after a
plugin lookup has failed.

### `POST /api/v1/commands`

Run a catalog command. For an installed plugin command, both
`"starter:review"` and `"/starter:review"` are accepted in `command`:

```json
{
  "command": "starter:review",
  "args": "UI/HTTP-API.go --Keep-Case MixedCase-v2",
  "session_id": "optional-existing-session-id"
}
```

Plugin command execution creates a session when `session_id` is omitted. A
supplied session must exist, otherwise the endpoint returns `404`. It expands
the command prompt using `args` and runs the real Agent Loop with the host's
configured Agent limits and approval policy. The argument tail retains its
spelling, case, and hyphens. This endpoint accepts text command arguments;
use `/api/v1/chat/sync` when the command turn includes attachments or per-request
chat options.

Successful plugin command execution returns HTTP `200`:

```json
{
  "command": "starter:review",
  "ok": true,
  "output": "assistant response",
  "session_id": "session-id"
}
```

The echoed command omits the optional leading slash and otherwise preserves the
namespaced name. Loop failures return HTTP `200` with `ok: false`, the error in
`output`, and the session id; clients must inspect `ok`. Invalid JSON returns
`400`. Built-in commands retain their existing command-specific behavior and
use the same `command`, `ok`, and `output` response fields.

## Plugin management

All plugin management routes use the existing API authentication and admission
policy. They manage installations on the server. The running Agent keeps its
startup snapshot until restart; these routes do not execute plugins or restart
the service.

| Method | Route | Description |
| --- | --- | --- |
| `GET` | `/api/v1/plugins` | Installed plugins, including disabled or damaged entries |
| `POST` | `/api/v1/plugins` | Install from a Git repository or server directory |
| `GET` | `/api/v1/plugins/{name}` | One installed plugin |
| `POST` | `/api/v1/plugins/{name}/enable` | Save enabled state |
| `POST` | `/api/v1/plugins/{name}/disable` | Save disabled state |
| `POST` | `/api/v1/plugins/{name}/update` | Update from the recorded source |
| `DELETE` | `/api/v1/plugins/{name}` | Remove registration, retaining immutable cache |

Inventory response:

```json
{
  "count": 1,
  "changes_require_restart": true,
  "plugins": [{
    "name": "starter", "version": "1.0.0", "description": "Review helpers",
    "source": "https://github.com/team/plugins.git", "ref": "v1.0.0",
    "revision": "resolved-commit", "subdir": "plugins/starter",
    "digest": "installed-sha256", "installed_at": "2026-10-18T04:00:00Z",
    "enabled": true, "trusted": false, "requires_trust": false,
    "skills": ["review"], "commands": ["review"], "agents": ["reviewer"],
    "mcp_servers": [], "hook_count": 0
  }]
}
```

Damaged entries include `error` and remain manageable. A corrupt installation
index fails the request rather than pretending the inventory is empty. A missing
installation directory returns an empty inventory. Detail returns `{plugin:...}`.
Responses omit prompt contents, scripts, MCP commands/environment and credentials;
source URL userinfo/query/fragment are redacted.

Install request, limited to 16 KiB:

```json
{"source":"https://github.com/team/plugins.git","ref":"v1.0.0","subdir":"plugins/starter","trust":false}
```

`source` is required. `ref`/`subdir` are optional. Local directories refer to the
server filesystem, not the Android device. Update accepts `{"trust":false}`
(4 KiB maximum), reuses the recorded source/ref/subdir, and retains a manually
disabled state. Trust is explicit for each installation version; it is not carried
over from previous contents. Untrusted MCP/hook plugins install disabled, while
pure prompt plugins can be enabled without execution trust. Enable/disable take
no request fields (clients may send `{}`). Git/copy operations have a 90 second deadline.

Install returns `201`; the remaining successful mutations return `200`:

```json
{"action":"install","name":"starter","plugin":{"name":"starter","version":"1.0.0"},"runtime_restart_required":true,"message":"插件变更已保存，重启 LuckyAgent 服务后生效。"}
```

The abbreviated `plugin` above uses the complete inventory entry in actual
responses; remove returns `plugin:null`. Names are lowercase slugs, 1–48 characters.
Invalid requests return `400`, oversized JSON `413`, unsupported media types `415`,
missing plugins `404`, unsupported methods `405`,
busy/conflicting trust or digest `409`, storage failures `500`, and deadlines `504`.
See [the plugin guide](plugin-module.md#android-插件管理) for the Android entry point
and restart behavior. No new `config.json` keys are required.

## Sessions

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/sessions` | List sessions |
| `POST` | `/api/v1/sessions` | Create a session |
| `GET` | `/api/v1/sessions/{id}` | Get a session (paged history + optional truncation) |
| `PATCH` | `/api/v1/sessions/{id}` | Rename, pin, or set the project |
| `DELETE` | `/api/v1/sessions/{id}` | Delete the session and its files |
| `GET` | `/api/v1/sessions/{id}/tools` | Get a bounded, paged tool trace for a session |
| `POST` | `/api/v1/sessions/{id}/compact` | Compact a session |
| `GET` | `/api/v1/sessions/{id}/compact/latest` | Get latest compact trace |

List query parameters:

| Parameter | Description |
| --- | --- |
| `q` | Search sessions |
| `limit` | Maximum results, capped by server |
| `offset` | Pagination offset |

List items include `format` (`legacy_md` or `segment_v1`) and `byte_size` when known. `pinned` is true for pinned sessions. `project` is the display group; empty means ungrouped. Pinned sessions sort before the rest. Search `q` also matches title, id, and project.

List/search items and single-session GET/PATCH responses also include optional
`recap` (one sentence in the conversation's language) and `recap_updated_at`
(RFC3339 timestamp). Older sessions omit them. The Agent generates this preview
asynchronously after a completed turn; an immediate read may still show the
previous preview. Failed generation preserves the previous value. Clients
display stored text and never derive it from history, compact summaries, memory
or RAG. Recaps do not replace titles or change conversation ordering. See
[session recaps](session-recap.md) for lifecycle and client behavior.

Patch body fields are optional, but at least one is required:

```json
{"title":"新名字","pinned":true,"project":"luckyagent"}
```

An empty `title` is rejected. An empty `project` clears the group. Delete returns `{"id":"...","deleted":true}`.

Metadata updates persist the sidecar without loading or rewriting the full
history. A successful delete invalidates existing session handles and cancels
active calls owned by this Agent, including this server's HTTP and WebSocket
work. Accepted WebSocket queue items remain bound to the original handle and
cannot recreate it after deletion. A filesystem failure returns an error and
does not trigger task cancellation or clear the manager's live entry. External
work stops when its provider or tool observes cancellation.

Get-session query parameters:

| Parameter | Description |
| --- | --- |
| `limit` | Page size (newest-first paging). Without `limit`, full message list is returned. |
| `offset` | Messages skipped from the newest end |
| `include=full` or `full=1` | Return full message bodies. Default truncates each body to `context.history_preview_chars` (default 4000 runes). |
| `all=1` | Legacy full history helper used by some clients |

Response extras: `format`, `byte_size`, `content_truncated` (true when bodies were capped).

History reads and tool traces return `500` for storage/decoding failures instead
of an empty history. A previously fetched handle invalidated by concurrent
deletion returns `410`; an unknown session ID returns `404`. Cold segment pages
build a bounded sparse offset index on first access and reuse it while the file
fingerprint is unchanged. Full-history requests still load the transcript.

Tool-trace query parameters:

| Parameter | Description |
| --- | --- |
| `limit` | Number of newest tool calls to return; defaults to 100 and is capped at 200 |
| `offset` | Tool calls skipped from the newest end |
| `turn=first` | Restrict to the first stored user turn of an execution session, excluding later follow-up conversations |

Tool trace responses include `total_calls`, `returned`, `has_more`, `limit`, and
`offset`, plus `turn` and `pending_calls`. Each call exposes its provider `id`
when available and `status` (`pending`, `completed`, or `failed`). A pending call
has no recorded result; this does not prove it is still executing. Success and
failure counters and `success_rate` count only calls with recorded results.
Clients requesting `turn=first` should check the echoed `turn` field rather than
assuming an older server supports the filter. If legacy compaction has removed
the original turn, the filter returns `410` rather than exposing later work.
Arguments, results, annotations,
and errors are truncated before serialization so a single tool output cannot
create an unbounded response. Persisted traces do not include live internal
reasoning or timing that was not recorded.

Create request body:

```json
{
  "title": "optional title"
}
```

Compact request body:

```json
{
  "dry_run": true,
  "force_local": true
}
```

Compact response includes token estimates, `summary_source`, and a client-oriented
`display` object (`title`, `subtitle`, `message`).

During streaming chat (`POST /api/v1/chat` SSE or WebSocket), automatic compact
emits events with `type: "compact"` and a payload:

```json
{
  "phase": "start|progress|done|failed|degraded",
  "trigger": "auto",
  "message": "Compressing conversation context…",
  "pre_token_estimate": 12000,
  "post_token_estimate": 900,
  "dropped_messages": 40,
  "retained_messages": 12,
  "summary_source": "llm-mapreduce",
  "boundary_id": "compact-…"
}
```

WebSocket message type is also `"compact"` with the same fields under `data`.

### Session storage CLI

Runtime session hygiene (not HTTP):

```bash
lh session list
lh session migrate <id>          # segment_v1 + blob externalize
lh session migrate --all
lh session gc <id>               # drop unreferenced blobs
lh session gc --all
lh session export <id> -o out.md # human-readable markdown
lh session roll <id> [--force]   # archive older turns, keep recent
lh session compact <id>
```

Related config keys under `context`:

- `session_auto_roll`, `session_max_messages`, `session_max_bytes`, `session_roll_retain_turns`
- `history_preview_chars`

New backups embed external message bodies; existing version 1 backups remain
readable when their bodies are available. Restore forces a complete transcript
rewrite, and GC retains references from live history, archives and backups.
Unreadable reference sources stop GC before deletion. Readers preserve complete
history before an incomplete uncommitted JSONL tail; missing committed messages
and middle corruption remain errors. The next append repairs that tail.
See [session storage reliability](session-reliability.md). No configuration keys
were added for these guarantees.

## Tasks

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/tasks` | List runtime tasks |
| `GET` | `/api/v1/tasks/{id}` | Get task detail |
| `GET` | `/api/v1/tasks/{id}/events` | Get task events |
| `GET` | `/api/v1/tasks/{id}/trace` | Get task trace |
| `GET` | `/api/v1/tasks/{id}/observation` | Get task observation |
| `POST` | `/api/v1/tasks/{id}/feedback` | Submit task feedback |
| `POST` | `/api/v1/tasks/{id}/cancel` | Cancel task |

List query parameters:

| Parameter | Description |
| --- | --- |
| `source` | Filter by task source |
| `status` | Filter by task status |
| `parent_id` | Filter by parent task |
| `limit` | Maximum tasks to return |

Feedback request body:

```json
{
  "status": "completed",
  "verified": true,
  "verifier": "user",
  "user_feedback": "accepted",
  "score": 0.9,
  "recommended_next": "finalize"
}
```

## Background Autonomy

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/autonomy/dashboard?state=&limit=200` | Read-only queue, worker, heartbeat and task snapshot |
| `GET` | `/api/v1/autonomy/tasks/{id}` | Read-only background task detail |

The dashboard never starts or changes the autonomy runtime. `state` accepts
`ready`, `in_progress`, `blocked`, or `done`. Task details expose operation
summaries and whether a checkpoint exists, but not the full checkpoint payload.

Cancel request body:

```json
{
  "reason": "stop now"
}
```

## Memory

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/memory` | Memory summary |
| `POST` | `/api/v1/memory` | Save a memory |
| `GET` | `/api/v1/memory/recall?q=...` | Recall memories |
| `GET` | `/api/v1/memory/stats` | Memory statistics |

Save request body:

```json
{
  "content": "Remember this",
  "category": "project",
  "long_term": false
}
```

Recall example:

```bash
curl "http://127.0.0.1:9090/api/v1/memory/recall?q=project"
```

## Configuration

Configuration routes use the normal API authentication middleware. When
`server.api_keys` is empty, they are available only from localhost or with a
process-local pairing key; otherwise send `X-API-Key` or `Authorization:
Bearer ...`.

### `GET /api/v1/config`

Returns the canonical configuration snapshot used by the runtime. API keys,
tokens, passwords and extra headers are redacted. The response includes an
`ETag` that can be sent as `If-Match` with a later PATCH.

### `PUT /api/v1/config`

Replaces the complete configuration. The body is limited to 2 MiB and is
validated before it is persisted and applied. Clients should GET the current
snapshot, modify it, and then PUT the complete document; PUT is not a partial
update. Empty redacted secrets keep their existing value, so use PATCH with
`clear_secrets` when a credential must be removed.

### `PATCH /api/v1/config`

Applies a JSON Merge Patch to the current configuration. The body can be the
patch object directly:

```json
{
  "agent": {"max_iterations": 20},
  "context": {"max_history_turns": 80}
}
```

For a dry run, optimistic concurrency, or explicit secret removal, use the
envelope form:

```json
{
  "patch": {"server": {"progress_summary_with_llm": true}},
  "if_match": "<etag>",
  "clear_secrets": ["models.endpoints.chat.api_key"],
  "dry_run": false
}
```

The response contains the redacted `config`, changed paths, `hot_reloaded`,
`restart_required`, `applied`, `persisted`, and a new `version`. A stale
`If-Match` returns `409`. Changes to server listening/authentication, gateways,
and startup-created services are persisted but require a restart or service
rebuild; the response reports those groups.

### `GET /api/v1/config/schema`

Returns field metadata for configuration clients: JSON path, type, default,
whether the field is sensitive, and whether it is hot-reloadable or requires a
restart. Sensitive defaults are always `null`. The response also groups paths
under `hot`, `restart`, and `unknown`.

### `POST /api/v1/config/reload`

Reload the active configuration without interrupting in-flight requests. The
response lists configuration groups applied to later requests and groups that
still require a process restart. Invalid or unsupported provider configuration
is rejected and the previous configuration remains active.

Sensitive values, including API keys, are never returned.

## Tools, Stats, Soul, Health

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/tools` | List available tools |
| `GET` | `/api/v1/stats` | Server statistics |
| `GET` | `/api/v1/soul` | Current SOUL information. `name` is the persona display name parsed from the Identity `Name:` line, then from the first bold name. `system_prompt` is the full SOUL text. |
| `GET` | `/api/v1/proactive/status?limit=5` | Proactive runtime status |
| `GET` | `/api/v1/health/live` | Liveness check |
| `GET` | `/api/v1/health/ready` | Readiness check |
| `GET` | `/api/v1/health/detail` | Detailed health information |
| `GET` | `/api/v1/metrics` | Metrics snapshot |

## Context

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/context` | Context window configuration. `inspect=1` adds the assembled composition and a local token estimate |
| `POST` | `/api/v1/context/fit` | Fit caller-supplied messages into the context window. This does not assemble the live planner context |

`GET /api/v1/context` accepts:

| Parameter | Description |
| --- | --- |
| `session_id` | Use this session's history and include its latest compact trace. Unknown ids return 404 and do not create a session |
| `inspect` | `1`, `true`, or `yes` adds `usage` and `sections`. Omitted responses stay config-only |
| `message` | Hypothetical next user message. Empty inspects the context already on hand and does not append a user turn |
| `include_messages` | `1` copies full section text into `sections[].content`. The default returns a 160-character `preview` |

`usage` is a local planner estimate, not billed provider usage. `usage.ratio` is `total_tokens / available_tokens`. Buckets are `system`, `project`, `history`, `memory`, `rag`, `tool_result`, and `user`. Inspection does not write memory, session messages, or the context cache.

Fit request body:

```json
{
  "messages": [
    {
      "role": "user",
      "content": "hello",
      "priority": 1,
      "category": "chat"
    }
  ],
  "strategy": "sliding_window"
}
```

Supported `strategy` values:

```text
oldest_first
low_priority_first
sliding_window
summarize
```

## RAG

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/rag/index` | Index a document |
| `DELETE` | `/api/v1/rag/index` | Remove indexed content |
| `POST` | `/api/v1/rag/search` | Search indexed content |
| `GET` | `/api/v1/rag/stats` | RAG statistics |
| `GET` | `/api/v1/rag/store` | Persistent store information |
| `POST` | `/api/v1/rag/store` | Store operation |

Index request body:

```json
{
  "source": "path-or-source-id",
  "title": "Document title",
  "content": "Document body",
  "dir": "optional-directory-path"
}
```

Indexing modes:

| Field | Behavior |
| --- | --- |
| `dir` | Index all supported files in a directory |
| `content` | Index inline text, using `source` and optional `title` |
| `source` | Index a single file path |

Delete request body:

```json
{
  "doc_id": "document-id"
}
```

Search request body:

```json
{
  "query": "programming language",
  "top_k": 5,
  "min_score": 0.1,
  "use_mmr": true,
  "mmr_lambda": 0.5,
  "source": "optional-source-filter"
}
```

## RAG Stream Indexer

These endpoints require the stream indexer to be initialized.

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/rag/stream/watch` | Add a watch directory |
| `DELETE` | `/api/v1/rag/stream/watch` | Remove a watch directory |
| `POST` | `/api/v1/rag/stream/scan` | Scan watched paths for changes |
| `POST` | `/api/v1/rag/stream/start` | Start background index workers |
| `POST` | `/api/v1/rag/stream/stop` | Stop background index workers |
| `GET` | `/api/v1/rag/stream/status` | Stream indexer status |
| `POST` | `/api/v1/rag/stream/index` | Index a path immediately |
| `DELETE` | `/api/v1/rag/stream/index` | Remove a path from the index |
| `GET` | `/api/v1/rag/stream/queue` | List queued jobs |
| `POST` | `/api/v1/rag/stream/process` | Process queued jobs |

Watch request body:

```json
{
  "dir": "F:/DevHub/Projects/2026-myapp/luckyagent/docs"
}
```

Index request body:

```json
{
  "path": "F:/DevHub/Projects/2026-myapp/luckyagent/docs/API.md"
}
```

Process request body:

```json
{
  "batch": 10
}
```

## Function Calling

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/fc` | Function calling endpoint summary |
| `POST` | `/api/v1/fc` | Execute a function call |
| `GET` | `/api/v1/fc/tools` | List function tools |
| `GET` | `/api/v1/fc/history` | Function call history note |

Execute request body:

```json
{
  "message": "Use available tools to inspect the repo",
  "auto_approve": false,
  "max_iterations": 8
}
```

## WebSocket

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/ws` | WebSocket realtime communication |
| `GET` | `/api/v1/ws/stats` | WebSocket hub statistics |

WebSocket URL:

```text
ws://127.0.0.1:9090/api/v1/ws?session=<session-id>
```

Authenticate the upgrade with the same headers as HTTP. Choose a stable `id`
for each chat request. Repeating the same session, ID and input returns the
accepted run and stored output without another execution. Different input
under that ID returns `REQUEST_CONFLICT`; failed initial persistence returns
`PERSIST_FAILED` without queueing a run. The running-state write must also
succeed before execution. A later event write failure reports `PERSIST_FAILED`,
stops execution and suppresses subsequent success output. This error is sent
without a replay cursor when the store itself is unavailable; the run is failed
rather than classified as user cancellation.

Send `type: "reconnect"` with `data.last_message_id` set to the last event
actually received. The server uses reliable bounded replay, buffers live
events during recovery, and deduplicates overlapping event IDs. If a client
cannot keep up it is explicitly disconnected and can retry from its cursor.
Clients should also deduplicate `event_id`. Recovery failures are reported as
`RECOVERY_FAILED` rather than an empty history.

`max_iterations` is applied to the actual Agent Loop and accepts 0 through 300.
Per-request `profile_id` is explicitly unsupported; use the model profile API
to change defaults. Partial or empty stream EOF is a failed run, not a completed
answer. Request deduplication does not make external tool side effects
transactional across a process crash.

Run persistence uses compatible JSON snapshots plus incremental JSONL journals
under the runtime home. Back up both. Older binaries cannot read new journal
entries after the last snapshot. Retention remains seven days, with active
work retained and cleanup performed on access/checkpoint.

Client message `type: "lucky"` collects several chat messages for one session
and submits them as a single turn. `data.action` is `on`, `off`, `status`, or
`cancel`. While collection is active, ordinary `chat` messages are stored
instead of starting a run. The server replies with `type: "status"` and
`data.state: "lucky"`. `off` enqueues one chat run built from the collected
segments. Collection state is in memory for the current server process.

On startup, durable run recovery scans session IDs and queued/running run
metadata from snapshots and journals without retaining all stored event
histories, including sessions with only a journal. Event replay still loads
a session lazily through the existing reconnect path. Run fields,
attachments, event JSON and reconnect cursors are unchanged. See
[runtime request and recovery options](runtime-request-recovery.md).

Server push `type: "task_event"` is sent to every connected client when a task
event is stored. `data` carries `type`, `task_id`, `parent_id`, `status`,
`mode`, `message`, `progress`, `session_id`, and `description`. A scheduled
message delivered into a session is still `type: "stream_end"` with no run id.

## gRPC embedding

`lh serve` does not start a gRPC listener. The application module's
`api/grpc.NewGRPCServer` library inherits Agent server keys and quotas and
protects unary/streaming methods, health and reflection. Embedders can supply
configuration and pairing validation with `NewGRPCServerWithOptions`.

Chat uses real sessions and execution statistics; ChatStream forwards live
Agent content with valid UTF-8 and one terminal Done. Errors and cancellation
return gRPC statuses. `max_iterations` accepts 0 through 300; nonempty metadata
is rejected because it has no defined semantics. The existing proto3 boolean
has no presence bit, so omitted `auto_approve` means false. The protobuf stream
continues to carry content/session/done fields rather than tool/approval events.

## Soul Templates

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/soul/templates` | List templates |
| `POST` | `/api/v1/soul/templates` | Create template |
| `GET` | `/api/v1/soul/templates/{id}` | Get or render template |
| `DELETE` | `/api/v1/soul/templates/{id}` | Delete template |

List accepts:

| Parameter | Description |
| --- | --- |
| `language` | Filter templates by language |

Render accepts query parameters prefixed with `var_`, for example:

```text
/api/v1/soul/templates/default?var_name=Alice&var_role=coder
```

## Embedders

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/embedders` | List embedders |
| `GET` | `/api/v1/embedders/{id}` | Get embedder |
| `POST` | `/api/v1/embedders/register` | Register embedder |
| `POST` | `/api/v1/embedders/switch` | Switch active embedder and rebind the RAG pipeline; a populated index must keep the same embedder identity and vector dimension |
| `POST` | `/api/v1/embedders/{id}/test` | Test embedder |

Switch request body:

```json
{
  "id": "embedder-id"
}
```

Register request body:

```json
{
  "id": "custom",
  "provider": "openai",
  "model": "text-embedding-3-small",
  "base_url": "https://api.openai.com/v1",
  "api_key": "optional",
  "dimension": 1536
}
```

Test request body:

```json
{
  "text": "hello"
}
```

## Multi-Agent Collaboration

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/agents` | List registered agents |
| `POST` | `/api/v1/agents/register` | Register an agent |
| `DELETE` | `/api/v1/agents/deregister?id=...` | Deregister an agent |
| `POST` | `/api/v1/agents/delegate` | Delegate a task |
| `GET` | `/api/v1/agents/task?id=...` | Get delegated task |
| `GET` | `/api/v1/agents/tasks` | List delegated tasks |
| `POST` | `/api/v1/agents/cancel?id=...` | Cancel delegated task |
| `GET` | `/api/v1/agents/blackboard?task_id=...` | Read shared collaboration notes |
| `PUT` | `/api/v1/agents/blackboard` | Write a task collaboration note |
| `DELETE` | `/api/v1/agents/blackboard?task_id=...&key=...` | Delete a collaboration note |
| `GET` | `/api/v1/agents/messages?task_id=...&from=...&to=...` | Read A2A collaboration messages |
| `POST` | `/api/v1/agents/messages` | Publish an A2A collaboration message |

Delegate request example:

```json
{
  "mode": "auto",
  "description": "Inspect three independent components and summarize risks.",
  "input": "Focus on this week's changes.",
  "agent_ids": ["api", "gateway", "ui"],
  "timeout": 60000000000,
  "cost_budget": 0.45
}
```

`mode` accepts `auto`, `parallel`, `pipeline`, or `debate`. `mdp` is not an
execution mode: it is the internal decision mechanism used by `auto` and is
rejected with `400 Bad Request`. `cost_budget` is optional and normalized to
the `0..1` range; `0` leaves planning cost unconstrained. `timeout` follows
the Go JSON duration representation used by this handler (nanoseconds).

Every delegated task and sub-task includes an `execution_session_id` when the
runtime can execute it. `parent_session_id` identifies the requesting session
and `role` identifies the runtime actor (for example `subagent` or
`orchestrator`). Open `GET /api/v1/sessions/{execution_session_id}` to inspect
the same transcript while the task is running and continue the conversation
after it completes. `GET /api/v1/sessions/{id}?profile=1` also returns the
runtime-owned `agent_role` and `agent_system_prompt` for that session.

See [the collaboration guide](multi-agent/collaboration.md) for selection
criteria and MDP learning behavior.

## Workflows

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/workflows` | List workflows |
| `POST` | `/api/v1/workflows` | Register workflow |
| `GET` | `/api/v1/workflows/{id}` | Get workflow |
| `DELETE` | `/api/v1/workflows/{id}` | Delete workflow |
| `GET` | `/api/v1/workflow-instances` | List workflow instances |
| `POST` | `/api/v1/workflow-instances` | Start workflow |
| `GET` | `/api/v1/workflow-instances/{id}` | Get workflow instance |
| `GET` | `/api/v1/workflow-instances/{id}/results` | Get workflow results |
| `DELETE` | `/api/v1/workflow-instances/{id}` | Cancel workflow instance |

Create workflow request body:

```json
{
  "name": "example",
  "description": "optional",
  "version": "v1",
  "tasks": []
}
```

Start workflow request body:

```json
{
  "workflowId": "workflow-id"
}
```

## Gateways

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/gateways` | List gateway statuses |
| `POST` | `/api/v1/gateways/{name}/start` | Start a configured gateway in the `lh serve` process |
| `POST` | `/api/v1/gateways/{name}/stop` | Stop gateway |
| `GET` | `/api/v1/gateways/{name}/status` | Gateway status |

The start endpoint accepts an optional JSON body. Credentials and listener
settings are read from `msg_gateway.<name>` in the host configuration; fields
sent by the caller override the matching values. This lets a remote client
start a configured QQ, NapCat, Feishu, Weixin, or OpenClaw Weixin gateway
without starting a second CLI process.

Telegram start request body (the token may be omitted when it is already in
the host configuration):

```json
{
  "token": "telegram-bot-token",
  "allowed_chats": ["123456"],
  "admin_ids": ["123456"]
}
```

For the other channels, send `{}` to use the host configuration. The gateway
instance belongs to the `lh serve` process and is stopped automatically when
that process shuts down. Do not run the same channel in a separate
`lh msg-gateway start` process at the same time; the Telegram API start is
guarded by the same local lease used by the CLI. The endpoint returns `409`
when the named gateway is already running.

## pprof

pprof is separate from `/api/v1`. It is enabled only when `LA_PPROF_ADDR` is set.

Example:

```bash
$env:LA_PPROF_ADDR="127.0.0.1:6060"
lh serve
```

Useful endpoints:

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/debug/pprof/` | pprof index |
| `GET` | `/debug/pprof/goroutine?debug=2` | Goroutine stacks |
| `GET` | `/debug/pprof/profile?seconds=30` | CPU profile |
| `GET` | `/debug/pprof/heap` | Heap profile |
| `GET` | `/debug/pprof/trace?seconds=5` | Runtime trace |

Do not expose the pprof address to untrusted networks.
