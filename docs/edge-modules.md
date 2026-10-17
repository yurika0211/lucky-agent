# Messaging and API module boundaries

The messaging and API transport now have independent Go module sources:

- `github.com/yurika0211/luckyagent-gateway`: platform connections, message and
  media presentation, collector, and neutral `turn` contracts.
- `github.com/yurika0211/luckyagent-api`: HTTP policies and routes, SSE framing,
  WebSocket connections, run storage/replay, resource controllers, health, and
  generated gRPC protocol.

LuckyAgent owns application behavior and composes both libraries in process.
The new modules do not import LuckyAgent or instantiate an Agent.

| Host package | Ownership |
| --- | --- |
| `internal/gateway/*` | Compatibility aliases for existing imports |
| `internal/server` | Compatibility aliases for API application construction |
| `internal/websocket` | Compatibility aliases for wire types and the host handler |
| `internal/turn` | Neutral contract aliases; durable execution stays application-owned |
| `internal/channelapp/telegram`, `qqofficial` | LuckyAgent commands, model/session/config operations and chat presentation policy |
| `internal/channelapp/websocket` | Agent run execution, cancellation and approval integration; run records/replay come from the API module |
| `internal/apiapp` | Agent/domain operations, storage/configuration policy and resource lifecycle assembly; shared resource parsing/rendering lives in `luckyagent-api/resources` |
| `api/grpc` | Application gRPC implementation and protocol aliases |
| `internal/workflow` | Compatibility aliases for `luckyagent-tools/workflow` and `luckyagent-api/workflow` |

The workflow DAG kernel is owned by the tool module; its optional Gin request
handlers are owned by the API module. Host action registration and the existing
net/http application route assembly remain host-owned. Transport handlers do
not instantiate an Agent or choose its configuration. During joint development
use matching local module checkouts. Merge tools, update and merge API, then
update host dependencies to downloadable commit versions or release tags and
verify with `GOWORK=off`.

This extraction moves reusable transport implementations. It does not describe
all code formerly under `gateway` and `server` as transport: domain operations
continue to live in the application packages above. There is no duplicated
platform implementation or protobuf schema in LuckyAgent.

The v0.2.1 cleanup also moves Telegram command/tool/Skill presentation and
reasoning/progress cards to `luckyagent-gateway/telegram/presentation`. Gateway
session administration and model/config changes remain host policy. Shared
NapCat group-history values live in `luckyagent-tools/chat` and are aliased by
the platform adapter.

`luckyagent-api/resources` owns task/tree/feedback, approvals, cron, autonomy,
Skill lifecycle/installation and collaboration request controllers. It consumes
public tool-runtime ports, with callbacks for host-owned installation policy
and orchestrator session creation. `internal/apiapp/resource_controller.go` is
the assembly point; it normalizes an absent Agent before interface injection.

Use a local, ignored `go.work` with all four module roots (adjust these paths to
the actual checkout locations):

```bash
go work init . ../luckyagent-gateway ../luckyagent-api ../luckyagent-tools
```

Workspace module selection keeps local changes together. The committed
`go.mod` contains no local replacement and pins the merged tools/API commits
as Go pseudo-versions. Downstream builds can download those versions without
workspace checkouts. Verify with `GOWORK=off` when upgrading to new commits or
release tags.

Validation:

```bash
# In each independent module
go test ./...

# In LuckyAgent
go test ./internal/apiapp ./internal/channelapp/... ./api/grpc ./internal/cli/lhcmd ./internal/agent
go test ./...
```

No new configuration key is introduced. Existing server, gateway and attachment
configuration remains in `config.example.json`. HTTP/WebSocket/gRPC protocol
names and platform permission checks retain their previous behavior.
