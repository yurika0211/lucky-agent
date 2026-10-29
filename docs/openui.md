# OpenUI slice

Fixes the contract in #369.

- Catalog: `luckyagent-runtime/openui`
- Agent helper: `EmitUIDelta` — validates, drops unknown nodes, returns
  `ChatEventUIDelta` or nil (Markdown path)
- Transports: `ui_delta` / `ui_action` on the WebSocket catalog
- Android: Compose names matching the catalog; streaming trees are not clickable

The model loop does not yet emit trees by itself. Call `EmitUIDelta` from a
future producer. Telegram/CLI keep Markdown until they implement the catalog.
