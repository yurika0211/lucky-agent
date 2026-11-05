# LuckyAgent application services

Public package `github.com/yurika0211/luckyagent-runtime/appservice` provides
session, model and configuration operations shared by command, HTTP, chat and
SDK entrances. It is part of the independently versioned runtime module. The
root package uses only the standard library; `profile` additionally uses YAML v3.
Host configuration/catalog adapters live in `luckyagent-app/appservice`.

## Public operations

| API | Responsibility | Caller supplies |
| --- | --- | --- |
| `Sessions[S]` | Exact ID, unique prefix, normalized title and optional list-index lookup; deletion followed by memory cleanup | `SessionStore[S]` and optional forget callback |
| `RenameSession`, `UpdateSessionMetadata` | Validation and correct full/metadata persistence method | Narrow mutable-session interfaces |
| `Models` | Copy-safe filtering, discovery/catalog merge and model selection | `ModelRuntime` and optional discovery methods |
| `SwitchLegacyModel`, `SaveSettings` | Ordered provider/model settings and staged partial failures | Provider-resolution, runtime-switch and settings ports |
| `Configuration[T]` | Prepare/preview, persist/apply and rollback with visible restoration errors | Store/runtime ports, clone/normalize/classify functions |
| `profile.Manager` | Startup Profile YAML, active marker and data-directory layout | Application home directory |

`S` is a comparable session handle, usually a pointer; its zero value means
missing. `T` is the caller's configuration schema. `ConfigurationPolicy.Clone`
must deep-copy mutable fields. Configuration read/prepare/commit operations
must be serialized by the caller. Legacy `SaveSettings` preserves Set/Save
semantics and offers no transactional rollback.

`ModelKind`, `ModelRef`, `SwitchModelOptions` and `SessionInfo` are neutral public
values. LuckyAgent aliases them at its existing import paths to preserve source
compatibility and JSON field names. Model credentials and concrete configuration
schemas stay with the caller. Startup Profiles and runtime model profiles remain
distinct concepts.

The package does not import the LuckyAgent host, tool, API or gateway modules.
It does not start an Agent, select credentials, own session message storage or
format HTTP/chat replies. Boundary tests check both source and test imports.

## Test independently

From the runtime module root:

```bash
GOWORK=off go test -mod=readonly -race ./appservice/...
GOWORK=off go vet ./appservice/...
```

Runtime CI runs these tests together with the module's other component suites.
The executable ExampleNewConfiguration demonstrates an application-owned schema.
