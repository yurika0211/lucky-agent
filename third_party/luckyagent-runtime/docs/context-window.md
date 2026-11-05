# Context budgets and complete tool exchanges

`bootstrap.ContextTokensFor` is the shared source for the Agent context window,
estimator and reply reserve. It selects positive `context.max_context_tokens`,
then legacy top-level `max_tokens`, then 8000. `ReservedTokensFor` applies
`context_budget.reserved_ratio` (default 0.25) to that same limit. The default
application input budget is therefore 6000 estimated tokens. `limits.max_tokens`
remains the independent model output cap.

JSON loading migrates a positive legacy `max_tokens` into the canonical context
field only when the input JSON has no positive `context.max_context_tokens`.
An explicit canonical value wins when both exist. `Manager.Set("max_tokens", …)`
updates both fields for existing CLI/UI callers. New callers should set
`context.max_context_tokens` directly. An SDK constructing a config in memory
must set the canonical field explicitly when it already has defaults.

The standalone `contextx.DefaultWindowConfig()` remains 4096/1024 for library
compatibility. Applications assembled through bootstrap use the shared config
above rather than that standalone default.

When `ContextWindow.Fit` trims, an assistant tool-call message and all matching
contiguous results form one group. Incomplete exchanges and orphan results are
discarded. The default `TrimLowPriority` selects critical groups first, then
high/normal/low groups from newest to oldest, and restores chronological order
for the returned messages. Other trimming strategies also remove partial tool
rounds from their output. Inputs already within budget pass through unchanged.

Critical groups are retained even when they exceed capacity. Callers must check
`TrimResult.FinalTokens` against `AvailableTokens` and reject an impossible
request instead of silently dropping pinned instructions.

Token estimates include message text, reasoning, names and tool-call arguments.
They are estimates, not a provider tokenizer. Tool schemas and image token
charges need additional accounting by the embedding host.

Validation covers canonical/legacy config precedence, the legacy setter,
bootstrap reserve alignment, atomic parallel tool rounds, recent-round
selection and visible critical overflow. Run:

```bash
GOWORK=off go test -mod=readonly -race ./config ./bootstrap ./contextx
```
