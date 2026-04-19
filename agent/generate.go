// Code generation entrypoint. Run `mise run spec` to regenerate everything.
// The tsp→openapi step runs on the host (needs pnpm); ogen runs in the
// agent container via `go tool ogen`.
package agent

//go:generate go tool ogen --target ./internal/admin/api --package api --clean ../spec/generated/openapi.yaml
