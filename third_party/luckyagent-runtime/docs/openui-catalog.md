# OpenUI catalog

Server-side names for structured assistant replies. This package validates
trees; it does not draw pixels.

Allowed types: `Table`, `Chart`, `List`, `Actions`, `Image`, `Svg`,
`Citation`, `Map`, `Markdown`.

`Actions` is `mutate`; the rest are `inspect`. Unknown components are
dropped. An empty remaining tree is not an error — callers keep Markdown.

Wire events live in luckyagent-api: `ui_delta` (patch) and `ui_action`
(intent, not tool execution). Android implements the same names in Compose.
