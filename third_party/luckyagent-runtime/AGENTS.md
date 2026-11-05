# Runtime component module

This module owns reusable backends, shared configuration and default resource
preparation. It must not import `lucky-agent/agent`, `luckyagent-app` or old
`luckyagent/internal` packages. The boundary test checks all Go imports.

- Use `git status --short` before editing and preserve unrelated changes.
- Keep session JSON, configuration fields and runtime-home layout compatible.
- Only `memoryaestus` imports the Aestus SDK. Use public memory ports elsewhere.
- `bootstrap.Open` prepares dependencies; `Start` is explicit. Keep initialization
  failures cleanup-safe and resource ownership/Close behavior documented.
- Do not silently combine provider middleware and generation retries.
- Run focused tests for the changed packages; changes spanning components use
  `GOWORK=off go test -mod=readonly -race -p 2 ./...` and `go vet ./...`.
- Configuration-field changes also require example and product documentation
  updates in the LuckyAgent launcher repository.
