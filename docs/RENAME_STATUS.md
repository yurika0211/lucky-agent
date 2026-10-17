# LuckyAgent Rename Status

## Current State

- Module path: `github.com/yurika0211/luckyagent`
- Runtime home: `${HOME}/.luckyagent`
- Database file: `luckyagent.db`
- Proto source: `api/proto/luckyagent.proto`
- gRPC service: `luckyagent.LuckyAgentService`
- Generated files under `api/grpc` are in sync with the proto source.

## Compatibility Kept

- `lh` remains the CLI command name.
- `LH_*` environment variables remain valid.
- Legacy manual environment variables and manual filenames are still read as
  fallback inputs by the prompt loader.

## Verification

```bash
go test ./...
cd UI && npm run typecheck
cd UI && npm run build
```
