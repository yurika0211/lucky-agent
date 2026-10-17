# Hardcoded Runtime Rules Audit

Date: 2026-07-10

## Scope

The audit covered Go runtime code, CLI and gateway startup, provider/model routing, memory and context routing, tool safety, and UI runtime defaults. Generated protobuf code, dependency lockfiles, benchmark/test fixtures, protocol endpoint paths, and user-facing documentation examples were scanned but are not treated as production rule debt by default.

The distinction used here is:

- **Decision rule debt**: literals decide intent, permissions, capabilities, routing, or user-specific behavior and cannot be changed without recompiling.
- **Protocol constant**: a stable command, endpoint, wire value, or file format marker required for interoperability.
- **Operational default**: a configurable fallback such as a loopback address or default timeout.

## Completed In This Change

The former domain-specific memory router in `internal/memory/memory.go` was removed. Child, pollen, outdoor, weather, air-quality, city, risk-priority, and tool-argument rules now live in typed `route_policies` on memory notes.

The memory gate no longer switches on `web_search` or `current_time`. It consumes structured `RouteToolRequirement` values and only auto-executes tools registered with `PolicySafe=true`. Routing now applies the current turn scope before evaluating memory policies.

## Findings

| Priority | Location | Hardcoded decision | Risk | Recommended target |
| --- | --- | --- | --- | --- |
| P0 | `internal/agent/tool_execution_guard.go:37,74` | User restrictions are inferred from Chinese/English phrases, then enforced through a switch over concrete tool names. | New tools can bypass read-only/write/delete/network restrictions until manually added. Phrase misses affect a safety boundary. | Add typed tool effects such as `read`, `filesystem_write`, `delete`, `network_mutation`, `memory_write`, and `task_control`; evaluate a request policy against effects rather than names. |
| P0 | `internal/agent/shell_command_classifier.go:17` | Shell mutation detection uses a small command/token list. | PowerShell cmdlets, aliases, compound syntax, interpreter commands, and many write operations are missed. | Parse per-shell ASTs or execute through a restricted command plan with declared effects. Keep string classification only as defense in depth. |
| P1 | `internal/provider/catalog.go:38` | Model IDs, capabilities, context windows, and prices are compiled into the binary. | Catalog data becomes stale; incorrect capability or context data changes runtime behavior. The entry `gpt-5.4-mini-mini` is especially suspicious. | Load a versioned catalog resource, merge configured custom models, and prefer provider discovery where available. Treat prices as dated metadata. |
| P1 | `internal/agent/tool_intent_gating.go:67` | A large multilingual keyword tree chooses the model-visible tool set. | False positives can hide required tools; new tools require edits in several branches. It is disabled by default, which limits current exposure. | Replace with typed tool capabilities plus an explainable intent-policy result. Keep unknown intent fail-open. |
| P1 | `internal/memory/memory.go:1906,1932,2152` | Query aliases, built-in concepts, tags, and concept relationships are embedded in Go. | Recall ontology is language/domain limited and requires release cycles to update. | Move concepts and aliases to typed vault/config resources; build the graph from those resources. |
| P1 | `internal/agent/agent.go:2808,2850` and `internal/tool/memory_service.go:836` | Memory category and importance inference use duplicated keyword tables and numeric weights. | The two write paths can classify the same fact differently and drift over time. | Centralize a typed `MemoryClassification` service with confidence and reason fields; allow explicit values to win. |
| P1 | `internal/agent/context_planner.go:1186-1317` | History relevance contains benchmark-specific markers, family/allergy aliases, and literal irrelevance phrases. | Unrelated text can be retained while valid current history is dropped; behavior is difficult to extend across domains and languages. | Preserve the latest-turn invariant, but replace domain aliases with scored lexical/embedding relevance and structured message metadata. |
| P2 | `internal/agent/skill_router.go:16,24` | Stopwords and cross-language skill concept aliases are global Go maps. | Skill routing quality depends on a fixed ontology unrelated to installed skill metadata. | Let skills declare route terms/examples in their manifests and merge them into a runtime index. |
| P2 | `internal/config/config.go:443,488` | Model complexity and local-task routing use fixed keyword lists. | Short phrases can select the wrong cost/capability tier; language coverage is narrow. | Use typed task signals already produced by the agent/tool planner, with configured thresholds and an explainable fallback classifier. |
| P2 | `internal/memory/tidal_reranker.go:390,442` | Tidal intent tags and allowed intent pairs are fixed in code. | Learning only generalizes over the compiled domains and silently ignores new domains. | Derive features from normalized memory tags/categories or load a versioned feature schema. |
| P2 | `internal/tool/builtin_web.go:904` | City aliases map to a small IANA timezone table. | Unknown cities fall back to local time, which can look valid but be wrong. | Prefer explicit `timezone`; use a configurable location resolver or return an unresolved-location error. |
| P2 | `internal/cli/lhcmd/commands.go:847` | Gateway construction is one large platform switch. | Every adapter adds CLI coupling and duplicated registration/start behavior. | Register typed gateway factories keyed by platform, with platform-specific option decoding owned by each adapter. |
| P3 | `internal/agent/system_prompt.go:657` | Platform names map to prompt files and hardcoded fallback prompt bodies. | A new platform gets no delivery policy until core code changes. | Let gateway adapters expose a delivery-policy resource. Existing external prompt files already reduce this risk. |
| P3 | `internal/cron/nl_parser.go:19` | The natural-language schedule grammar is fixed to a small Chinese phrase set. | Limited language coverage, but failures are explicit rather than silently unsafe. | Keep as a bounded parser or move grammar tables to locale resources if broader language support is required. |

## Reasonable Constants

These were reviewed and should not be removed merely because they are literals:

- `/api/v1` routes, gateway commands such as `/lucky`, OneBot paths, SSE event names, Markdown frontmatter keys, and tool names are protocol contracts.
- `127.0.0.1:9090` in server/UI defaults is an overridable, security-conscious loopback default. The duplication should eventually share one build/runtime setting, but it is not a business rule.
- Provider base URLs such as the OpenAI and Ollama defaults are configurable interoperability defaults.
- Retry limits, context budgets, result limits, and timeouts are tuning defaults. They should be configurable when operators need control, but typing them as policies would not improve the design.
- Test and benchmark datasets intentionally contain fixed expected values and domain scenarios.

## Recommended Order

1. Type tool effects and replace the execution-guard tool-name switch.
2. Replace the shell token classifier with shell-aware parsing or restricted execution plans.
3. Externalize the model catalog and validate suspicious model metadata.
4. Consolidate memory classification and externalize the memory/skill ontologies.
5. Replace history and model-routing keyword heuristics after adding comparable quality benchmarks.
