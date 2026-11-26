# OpenUI slice

Fixes the contract in #369.

- Catalog: `luckyagent-runtime/openui`
- Agent helper: `EmitUIDelta` — validates, drops unknown nodes, returns
  `ChatEventUIDelta` or nil (Markdown path)
- Transports: `ui_delta` / `ui_action` on the WebSocket catalog
- Android: Compose names matching the catalog; streaming trees are not clickable

Free chat emits a tree by calling the `present_ui` tool. The handler validates
the tree with `EmitUIDelta`. A kept tree becomes `ChatEventUIDelta` on the
stream path and on the non-stream loop when the caller emits events. The raw
tree is dropped from the tool result the model sees next.

When that tree is delivered, the Markdown answer keeps the conclusion and
facts the tree does not contain. An accepted Table, Chart, or List is not
repeated as the same Markdown table, chart, or list. The receipt says so.
Telegram, CLI, TUI, and the other channels that do not implement the catalog
still get a receipt that asks for the full Markdown answer, including the table.
An empty or invalid tree stays on the Markdown path.

Chart lifecycle (#509) is not finished in this launcher. The versioned
`chart_id` / `data|teaching` contract is merged in runtime PR 44 and carried
on the wire by API PR 18; the nested Agent selects the merged runtime commit.
The app action PR adds durable chart version/idempotency state, while the
remaining session child-result and Android history work is still open. See
`docs/issue-509-chart-lifecycle.md`. Do not treat a prompt rule as the
version check.
