---
name: openui
description: Use when a free-chat answer is clearer as a table, chart, list, citation, map, or choices. Read this skill before calling present_ui so the tree uses allowed node types and props.
---

# openui

## When to use

When a free-chat answer is clearer as a table, chart, list, citation, map, or a small set of choices, call present_ui once with a catalog tree. Skip the tool for a plain sentence.

## Rules

On a client that renders OpenUI, a tree the tool accepts is already shown. Keep the Markdown answer to the conclusion and facts the tree does not contain. Do not repeat an accepted Table, Chart, or List as a Markdown table, chart, or list with the same rows. Citation, Map, Image, Svg, Markdown, and Actions nodes do not replace the prose around them.

Telegram, CLI, TUI, and any channel that does not render the catalog never shows the tree. Those channels still need the full Markdown answer, including the table. The tool result says which case this call is.

The tree is a JSON object: tree_id, state, nodes. Each node has id, type, and props. Allowed types and required props:

- Table: columns, rows
- Chart: kind, series. kind is bar|line|area|pie|scatter. series[].kind may differ so one chart mixes those kinds; missing or Chinese/unknown follows the node kind. pie cannot mix with cartesian — the client then draws pie only. Optional series[].yAxis "right". Optional range / brush crops already present points; dragging does not call the model. When the user applies, they re-ask via ui_action with values.window, optional kind, and optional start (brushed mid-span).
- List: items
- Actions: items (choices only; a write still goes through the existing approval flow). A choice is ui_action; the tree stays so the user can choose again.
- Image: src
- Svg: markup
- Citation: source
- Map: points
- Markdown: text

state is complete, or waiting_input when Actions needs a choice. Do not invent Box, Row, Carousel, AppBlock, or HTML as the tree.
