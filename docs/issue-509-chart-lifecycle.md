# Issue #509 chart lifecycle

Decision record for the Android chart task. This file does not claim the
loop is finished.

## What this repository can do now

`present_ui` validates a catalog tree and emits `ChatEventUIDelta`. The
published runtime still treats a Chart as `kind` + `series`. It does not
require `chart_id`, `schema_version`, `data_version`, or a `data` /
`teaching` mode. The Agent therefore cannot, by itself:

- refuse a stale `data_version`
- keep the previous chart when validation fails
- tell a real data chart from a teaching chart
- restore a chart after the session is reopened

Doing those checks only in the prompt would let a client ignore them.

## Contract, not yet selected by this launcher

The shared contract is proposed in:

- runtime: https://github.com/yurika0211/luckyagent-runtime/pull/44 (`fef22d0`)
- API wire fields: https://github.com/yurika0211/luckyagent-api/pull/18 (`a5f7e8e`)

Rules in that contract:

- `mode=data` is read-only, even if `editable` is true, and it requires `data_source`.
- `mode=teaching` is editable. A teaching chart marked read-only is dropped.
- A chart `ui_action` must carry `chart_id`, both versions, and `apply` or `observe`.
- Charts that omit the new fields stay on the old apply path.

This launcher must not replace its runtime module with that branch.
Select the commit only after the PR is merged and tagged.

## Still outside this repository

| Piece | Owner | Not done |
| --- | --- | --- |
| Persist the tree and the applied version | app / API | No child turn, no idempotent Apply |
| Reject an old `data_version` against stored state | app | The catalog only checks the shape |
| Recompute one `chart_id` | app | No algorithm boundary here |
| Long-press form, draft, cache | Android | Client must not infer edit rights |
| One chart per question, stable type | Agent prompt | Safe only after the contract is selected |

## Acceptance still open

Reopen, long-press, cancel-without-a-turn, one Apply, failed validation
keeping the last good chart, and teaching-point drag all need the app
and Android. Closing #509 from this repository alone would be false.
