# OpenUI slice

Fixes the contract in #369.

- Catalog: `luckyagent-runtime/openui`
- Agent helper: `EmitUIDelta` — validates, drops unknown nodes, returns
  `ChatEventUIDelta` or nil (Markdown path)
- Transports: `ui_delta` / `ui_action` on the WebSocket catalog
- Android: Compose names matching the catalog; streaming trees are not clickable

Free chat emits a tree by calling the `present_ui` tool. The handler validates
the tree with `EmitUIDelta`. A kept tree becomes `ChatEventUIDelta` on the
stream path; the non-stream loop keeps the Markdown answer and drops the raw
tree from the tool result the model sees next. An empty or invalid tree stays
on the Markdown path. Telegram/CLI keep Markdown until they implement the catalog.
