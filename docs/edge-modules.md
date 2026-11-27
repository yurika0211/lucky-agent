# Messaging and API module boundaries

The main repository now maintains the public Agent and thin launcher. Host
application packages described below have moved to `luckyagent-app`; support
components are public packages in `luckyagent-runtime`. See
[Agent core and launcher modules](core-modules.md) for current paths and builds.
The independent API/gateway transport ownership described here remains valid.

The messaging and API transport now have independent Go module sources:

- `github.com/yurika0211/luckyagent-gateway`: platform connections, message and
  media presentation, collector, and neutral `turn` contracts.
- `github.com/yurika0211/luckyagent-api`: HTTP policies and routes, SSE framing,
  WebSocket connections, run storage/replay, resource controllers, health, and
  generated gRPC protocol.

LuckyAgent owns application behavior and composes both libraries in process.
The new modules do not import LuckyAgent or instantiate an Agent.

Cross-entrance session, model and configuration operations are shared through
[independent application-service module](application-services.md). Domain handlers retain
request/reply mapping and runtime assembly; startup Profile persistence is
shared without making channel handlers depend on CLI packages.

| Host package | Ownership |
| --- | --- |
| `luckyagent-gateway` | Platform connections; the old `internal/gateway/*` aliases are gone |
| `luckyagent-app/server` | Compatibility aliases for API application construction |
| `luckyagent-app/websocket` | Compatibility aliases for wire types and the host handler |
| `luckyagent-runtime/turn` | Neutral turn contract; durable execution stays application-owned |
| `luckyagent-app/channelapp/telegram`, `qqofficial` | LuckyAgent commands, model/session/config operations and chat presentation policy |
| `luckyagent-app/channelapp/websocket` | Agent run execution, cancellation and approval integration; run records/replay come from the API module |
| `luckyagent-app/apiapp` | Agent/domain operations, storage/configuration policy and resource lifecycle assembly; shared resource parsing/rendering lives in `luckyagent-api/resources` |
| `luckyagent-runtime/appservice` | Independent session/model/configuration services and startup Profile persistence; no host imports |
| `luckyagent-app/appservice` | Type aliases and host configuration-schema/provider-catalog adapters |
| `api/grpc` | Application gRPC implementation and protocol aliases |
| `luckyagent-runtime/workflow` | Compatibility aliases for `luckyagent-tools/workflow` and `luckyagent-api/workflow` |

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
and orchestrator session creation. `luckyagent-app/apiapp/resource_controller.go` is
the assembly point; it normalizes an absent Agent before interface injection.

The committed workspace selects the launcher and its local `agent` leaf module.
For joint development, use an ignored go.local.work that also includes the
app/runtime checkouts. External modules use published versions by default and
do not require source checkouts or local replacements. Run each
changed module's own tests, and build the launcher with `GOWORK=off` after
updating pinned dependencies. See the core module guide for exact commands.

No new configuration key is introduced. Existing server, gateway and attachment
configuration remains in `config.example.json`. HTTP/WebSocket/gRPC protocol
names and platform permission checks retain their previous behavior.
