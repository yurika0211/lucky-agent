# Shared application services

Neutral shared operations live in `luckyagent-runtime/appservice`:

- `Sessions[S]`: exact ID, unique prefix, normalized title/index lookup and
  deletion followed by session-memory cleanup.
- Narrow rename/metadata writer ports preserve full versus metadata-only saves.
- `Models`: copy-safe filtering, discovery merging and model selection ports.
- `Configuration[T]`: prepare/preview, persist/apply and rollback with visible
  restoration failures, using caller-supplied clone/normalize/classify rules.
- `SaveSettings` and legacy model switching preserve ordered partial failures.
- `appservice/profile`: startup Profile YAML and active-marker storage.

The full application's schema/catalog adapters live in
`luckyagent-app/appservice`. CLI, HTTP, Telegram/QQ and SDK use those adapters;
Profile consumers import the runtime module's public Profile package directly.
The Agent core contains no application command handler.

Exact-ID precedence and ambiguous-prefix rejection, metadata-only persistence,
SDK short-memory cleanup after deletion, canceled provider discovery and config
rollback reporting remain shared behavior. HTTP retains locks, ETags, decoding,
permissions and presentation. Config read/prepare/commit callers serialize their
complete update sequence. Startup Profiles remain distinct from running model
profiles.

These services are public packages in the runtime module, not a nested module
inside the launcher repository. No local replace or extra appservice repository
is needed. Both runtime and app have their own independent tests and CI.

No endpoint, JSON field or runtime configuration key is added by this migration;
config.example.json retains the existing surface. See [the complete module
structure](core-modules.md) for construction, dependencies and executable builds.
