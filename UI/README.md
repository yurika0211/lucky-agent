# LuckyAgent UI

This workspace contains two independent TypeScript apps:

- `GUI`: Vite + React dashboard
- `TUI`: Ink-based terminal UI

## Install

```bash
cd UI
npm install
```

## Dashboard

Development server:

```bash
npm run dev --workspace GUI
```

Build for Go static hosting:

```bash
npm run build --workspace GUI
```

The Go dashboard serves `UI/GUI/dist` when present.

## TUI

```bash
npm run dev --workspace TUI -- --api-base http://127.0.0.1:9090 --session dashboard-main
```

If you hit an `ink` / `yoga-layout` module format error, use the ESM entry:

```bash
npm run start --workspace TUI -- --api-base http://127.0.0.1:9090 --session dashboard-main
```

## Root helpers

From the repo root:

```bash
make ui-install
make ui-build
make ui-dev
make ui-typecheck
```

On Windows PowerShell, use `npm run dev:tui` or `npm run start:tui` from `UI/` if `make` is not on PATH.
