# LuckyAgent Desktop (Electron)

Thin Electron shell around the existing React GUI (`UI/GUI`).

This package is intentionally minimal:

- load Vite dev server in development
- load `UI/GUI/dist` in production-like mode
- rounded frameless window (transparent shell + CSS radius; drag via native topbar/sidebar)
- app icon from four-leaf clover (`assets/icon.png`, sourced from `public/favicon.png`)
- expose a small `window.luckyDesktop` bridge
- do **not** rewrite GUI business logic

## Auto runtime

On launch, the desktop shell:

1. Starts `lh serve` on `LH_API_BASE` (default `http://127.0.0.1:9090`) if it is not already healthy
2. Serves the packaged GUI through a local UI gateway (default `http://127.0.0.1:8765`) so `/lh-api` and `/api/*` work
3. Lets the GUI probe health and auto-connect the WebSocket session

Disable auto-start with `LH_ELECTRON_AUTOSTART=0`.

## Status

Working local desktop shell:

- starts local `lh serve` when the API is down
- single-instance lock and tray
- Linux desktop entry installer
- optional Electron runtime bundle for release archives

Not yet:

- code signing
- auto-update
- macOS/Windows signed installers for the Electron shell

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

Production `file://` loading uses the GUI relative Vite base (`LH_GUI_BASE`, default `./`).

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `LH_ELECTRON_LOAD` | auto | `dev` or `dist` |
| `LH_ELECTRON_DEV_URL` | `http://127.0.0.1:5173` | Vite URL |
| `LH_API_BASE` | `http://127.0.0.1:9090` | Runtime API base exposed to preload |

## Install a Linux menu entry

From the repo root, after `npm install` in `UI/`:

```bash
bash scripts/install-desktop-entry.sh
```

This installs `~/.local/share/applications/luckyagent.desktop`, copies the clover icon, and creates `~/.local/bin/luckyagent-desktop`.

## Package the shell into a release bundle

```bash
bash scripts/build-desktop-bundle.sh dist/release-root
```

The launcher is `luckyagent-desktop`. It loads `UI/GUI/dist` with relative asset paths and starts `lh serve` if `127.0.0.1:9090` is not already healthy.

## Next steps

1. Code signing for macOS and Windows Electron binaries
2. Auto-update
3. Keep desktop/web/installer icons synced from the clover source (`UI/GUI/public/favicon.png`)

## Release packaging

Release tags (`v*`) build installers that include the Electron desktop shell when `UI/desktop` exists:

- Linux: `.tar.gz` + `.deb` include `luckyagent-desktop`, `desktop/`, and `runtime/electron/`
- Windows: `LuckyAgent-Setup-*.exe` includes Desktop shortcut/launcher + Electron runtime
- macOS: `.tar.gz` + `.pkg` include `luckyagent-desktop` launcher

Requirements in CI:

1. `npm ci` under `UI/` (workspaces install `@luckyagent/desktop` → `electron`)
2. `scripts/prepare-release-bundle.sh` / `scripts/build-windows-installer.ps1` stage the shell
3. Packaging fails if Electron runtime or desktop sources are missing

Still not included:

- code signing
- auto-update
- notarized macOS / Authenticode-signed Windows packages
