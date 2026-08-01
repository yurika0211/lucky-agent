# LuckyAgent HTTP API

This document describes the HTTP API registered by `lh serve`.

Source of truth: `internal/server/server.go` and the handler files under
`internal/server/`. If behavior differs from this document, verify the matching
handler before relying on it.

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

If `server.api_keys` is empty, authentication is disabled.

## Common Response Shape

Most successful responses are JSON. Error responses use:

```json
{
  "error": "message",
  "code": 400,
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
| `429` | Rate limit exceeded |
| `500` | Internal error |
| `503` | Service not initialized |

## Chat

### `POST /api/v1/chat`

Streaming chat over Server-Sent Events.

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

### `POST /api/v1/chat/sync`

Synchronous chat response.

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

## Sessions

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/sessions` | List sessions |
| `POST` | `/api/v1/sessions` | Create a session |
| `GET` | `/api/v1/sessions/{id}` | Get a session |
| `POST` | `/api/v1/sessions/{id}/compact` | Compact a session |
| `GET` | `/api/v1/sessions/{id}/compact/latest` | Get latest compact trace |

List query parameters:

| Parameter | Description |
| --- | --- |
| `q` | Search sessions |
| `limit` | Maximum results, capped by server |
| `offset` | Pagination offset |

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

## Tools, Stats, Soul, Health

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/tools` | List available tools |
| `GET` | `/api/v1/stats` | Server statistics |
| `GET` | `/api/v1/soul` | Current SOUL information |
| `GET` | `/api/v1/proactive/status?limit=5` | Proactive runtime status |
| `GET` | `/api/v1/health/live` | Liveness check |
| `GET` | `/api/v1/health/ready` | Readiness check |
| `GET` | `/api/v1/health/detail` | Detailed health information |
| `GET` | `/api/v1/metrics` | Metrics snapshot |

## Context

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/context` | Context window configuration |
| `POST` | `/api/v1/context/fit` | Fit messages into context window |

`GET /api/v1/context` accepts:

| Parameter | Description |
| --- | --- |
| `session_id` | Include latest compact trace for the session when available |

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
| `POST` | `/api/v1/embedders/switch` | Switch active embedder |
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

Register and delegate bodies use the collab package models. Verify
`internal/server/collab_handlers.go` and `internal/collab` before treating
those JSON shapes as stable external contracts.

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
| `POST` | `/api/v1/gateways/telegram/start` | Start Telegram gateway |
| `POST` | `/api/v1/gateways/{name}/stop` | Stop gateway |
| `GET` | `/api/v1/gateways/{name}/status` | Gateway status |

Telegram start request body:

```json
{
  "token": "telegram-bot-token",
  "allowed_chats": ["123456"],
  "admin_ids": ["123456"]
}
```

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
