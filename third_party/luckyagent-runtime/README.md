# LuckyAgent runtime components

`github.com/yurika0211/luckyagent-runtime` provides the public components used
by the Agent core and the full LuckyAgent application. It does not import the
Agent or application modules.

| Packages | Ownership |
| --- | --- |
| `provider`, `middleware`, `resilience` | Model protocols, discovery, catalogs, streaming, fallback and call governance |
| `session` | JSONL history, blobs, pagination, backups, migration and compact boundaries |
| `rag`, `embedder` | Retrieval, embeddings, graph/vector/SQLite and stream indexing |
| `memoryaestus` | Aestus adapter, session buffers/summaries, maintenance and optional Tidal resources |
| `config` | Existing shared configuration schema, defaults, migration, persistence and reload classification |
| `bootstrap` | Default component preparation, explicit application activation and owned resource cleanup |
| `appservice`, `appservice/profile` | Neutral session/model/config operations and startup Profile persistence |
| `mcpclient`, `credentials`, `proactive` | MCP connections, credential backend and proactive prediction/feedback |
| `contextx`, `hook`, `soul`, `logger`, `metrics`, `telemetry` | Supporting runtime capabilities |
| `tool`, `turn`, `gateway`, `health`, `workflow` | Compatibility bindings to existing tool and transport modules |

The existing configuration fields, conversation JSON fields and runtime-home
layout are preserved. The Aestus SDK is imported only from `memoryaestus`;
consumers use its public memory ports. The execution kernels and generic tools
remain in `luckyagent-tools`; API/platform protocols remain in the independent
API and gateway modules.

[Session storage and recovery](docs/session-storage.md) covers concurrent
saves, deletion, standalone backups, incomplete JSONL tails, metadata-only
updates, bounded history pagination and optional user-facing recap metadata
with stale-write protection. Recap generation belongs to the Agent.

## Resource lifecycle

`bootstrap.Open(configManager, optionalMemoryRuntime)` prepares dependencies.
It does not start MCP connections, proactive workers or Agent execution.
`Components.Start(ctx, Hooks)` activates the application once using injected
callbacks. `Close` is idempotent and also handles partial preparation. A closed
component set cannot be restarted. Applications serialize lifecycle calls.

The public Agent's `NewWithDependencies` consumes these prepared resources.
Default application startup lives in `luckyagent-app/bootstrap`. Resources stay
with their creator unless ownership is explicitly transferred.

`mcpclient.Manager.Configure` lets an application supply a validated declarative
catalog before explicit activation, including trusted plugin servers. It starts
no connections and leaves active sessions unchanged; see
[MCP configuration lifecycle](docs/mcp-lifecycle.md).

## Provider options

[Context windows](docs/context-window.md) describes the canonical input budget,
legacy config migration, reply reserve and complete tool-exchange trimming.

[Prompt caching request options](docs/prompt-caching.md) describes stable tool
schemas and the optional `models.endpoints.chat.prompt_cache_key`, including
the independent per-fallback setting and both OpenAI protocols.

[Provider fallback](docs/provider-fallback.md) describes model-level cooldown,
shared-endpoint failures, per-node circuit breakers and the readiness check.

## Validation

```bash
GOWORK=off go test -mod=readonly -race -p 2 ./...
GOWORK=off go vet ./...
```

The module import-boundary test rejects dependencies on the Agent, the
application and the former host's internal packages, including in tests.
