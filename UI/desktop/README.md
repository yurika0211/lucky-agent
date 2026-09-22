# LuckyAgent Desktop (Electron)

Thin Electron shell around the existing React GUI (`UI/GUI`).

This package is intentionally minimal:

- load Vite dev server in development
- load `UI/GUI/dist` in production-like mode
- rounded frameless window (transparent shell + CSS radius + custom titlebar)
- expose a small `window.luckyDesktop` bridge
- do **not** rewrite GUI business logic

## Status

Scaffold only. Not yet:

- auto-start `lh` runtime
- tray / autostart
- code signing / installers
- auto-update

## Setup

From `UI/`:

```bash
npm install
```

Electron is installed as a workspace dependency of `@luckyagent/desktop`.

## Develop

```bash
# from UI/
npm run dev:desktop
```

This starts:

1. GUI Vite on `http://127.0.0.1:5173`
2. Electron window pointing at that URL

Requires a running LuckyAgent API (default `http://127.0.0.1:9090`), same as the web GUI.

## Run against built GUI

```bash
npm run build --workspace GUI
npm run start:dist --workspace @luckyagent/desktop
```

Note: production `file://` loading needs GUI assets built with relative base.
Use `LH_GUI_BASE=./ npm run build --workspace GUI` once the Vite base switch is in place.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `LH_ELECTRON_LOAD` | auto | `dev` or `dist` |
| `LH_ELECTRON_DEV_URL` | `http://127.0.0.1:5173` | Vite URL |
| `LH_API_BASE` | `http://127.0.0.1:9090` | Runtime API base exposed to preload |

## Next steps

1. Make GUI Vite `base` relative for `file://` packaging
2. Optionally spawn/manage local `lh` process from main
3. Add `electron-builder` packaging matrix later
