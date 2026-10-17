# Standalone tool module

`github.com/yurika0211/luckyagent-tools` owns the reusable tool implementation.
LuckyAgent embeds this Go module in process and supplies application services.

| Location | Ownership |
| --- | --- |
| `luckyagent-tools` root | Tool contracts, registry, routing, execution gateway, generic builtins, Skills, MCP and usage tracking |
| `luckyagent-tools/search` | Search engines and aggregation |
| `luckyagent-tools/sandbox` | Execution snapshots and shell/path boundaries |
| `luckyagent-tools/opencli` | Shared OpenCLI configuration shape |
| `luckyagent-tools/textutil` | Shared text/HTML/URL formatting |
| `luckyagent-tools` runtime services | Memory/RAG argument handling, delegation, scheduling, media/computer tools, temporary credential execution and Codex/Grok bridges |
| `luckyagent-tools/{task,collab,cron,autonomy}` | Optional generic engines, storage and injected execution callbacks |
| `luckyagent-tools/{memory,rag,credentials,chat}` | Neutral ports, shared values and validation |
| `luckyagent-tools/{computer,multimodal,codex,grok}` | Optional providers and external clients |
| `luckyagent-tools/{approval,hitl}` | Shared approval values and runtime-scoped input/approval waits |
| `luckyagent-tools/skillroute` | Matching skill snapshots; host owns routing exceptions and model-tool choice |
| `luckyagent-tools/execpolicy` | Pure intent allowlists and turn-local tool constraints |
| `luckyagent-tools/compact` | Chunking, parallel summaries, validation, fallback and restore evidence |
| `luckyagent-tools/workflow` | DAG engine with injected action handlers |
| `luckyagent-api/workflow` | Optional Gin workflow request handling |
| `internal/toolapp` | Per-call credential-store opening and cleanup; real-backend integration tests |
| `internal/tool`, `internal/tool/search` | Existing import paths forwarded to the owning module or application adapter |
| `internal/sandbox`, selected `internal/utils` helpers | Aliases to the single shared implementation |

The standalone module does not construct an Agent, open LuckyAgent's memory
backend or import LuckyAgent. Memory/RAG services consume backend interfaces.
Approval prompting remains in the embedding application. Tool names, JSON
parameters, permission levels, filesystem defaults and sandbox behavior retain
their existing semantics.

Task execution and provider choice remain in Agent assembly. The credential
adapter implements `CredentialProvider`; the host retains encrypted storage.
The Aestus SDK is still imported only by `internal/memoryaestus`; memory ports
now have one owner in `luckyagent-tools/memory`. RAG storage, embedding and
retrieval remain under `internal/rag`, implementing the SDK's two-method port.

The original `OpenCLIConfig` and `ComputerUseToolConfig` alias module DTOs.
It retains the same fields and JSON tags. No new configuration key is needed
in `config.example.json` or the configuration website.

## Local joint development

Create an ignored workspace when editing the execution components across
repositories (replace the paths with the actual checkouts):

```bash
go work init . /path/to/luckyagent-tools /path/to/luckyagent-api /path/to/luckyagent-gateway
```

The committed `go.mod` pins merged tools/API commits as downloadable Go
pseudo-versions and has no local `replace`. Normal checkouts can build with
`GOWORK=off`; a workspace is only needed for local changes across repositories.
New execution packages are not in tools v0.2.2/API v0.2.1. For future changes,
merge tools first, update/merge API, then update the host requirements to the
merged commits or newer release tags. Approval rendering, model/credential selection,
automatic compaction policy, session persistence and actual dispatch remain
host-owned. Shared compaction DTO aliases keep existing JSON fields unchanged.

## Validation

```bash
# In luckyagent-tools
go test ./...
go test -race ./...

# In LuckyAgent
go test ./internal/toolapp ./internal/config ./internal/utils ./internal/agent ./internal/apiapp ./internal/cli/lhcmd ./sdk
go test -p 2 ./...

# Verify downloadable dependencies without the local workspace
GOWORK=off go test -mod=readonly -p 2 ./...
```

All 552 original tool/search test entrypoints are retained exactly once across
the two repositories. Generic MCP tests no longer create an unused host
configuration manager. The standalone module additionally checks its import
boundary and executes its embedding example.

The v0.2.1 cleanup moves reusable runtime services and their tests, rather than
keeping a second implementation behind host aliases. Host callers import the
public capability packages directly. Legacy `internal/tool` names remain
compatible; its runtime builtin registrations forward to the SDK's media-aware
catalog. No configuration keys or tool wire names change.
