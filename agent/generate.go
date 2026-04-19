// Code generation entrypoint. Run `mise run spec` to regenerate everything.
// The tsp→openapi step runs on the host (needs pnpm); ogen runs in the
// agent container via `go tool ogen`.
package agent

//go:generate go tool ogen -config .ogen.yaml --target ./internal/api/admin/gen --package gen --clean ../spec/generated/admin/openapi.yaml
//go:generate go tool ogen -config .ogen.yaml --target ./internal/api/control/gen --package gen --clean ../spec/generated/control/openapi.yaml
