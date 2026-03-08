.PHONY: dev-admin dev-admin-api dev-admin-web build-admin proto api

# gRPC コード生成
proto:
	protoc \
		--go_out=. --go_opt=module=github.com/haryoiro/suzuha \
		--go-grpc_out=. --go-grpc_opt=module=github.com/haryoiro/suzuha \
		proto/consolidator/v1/consolidator.proto
	protoc \
		--go_out=. --go_opt=module=github.com/haryoiro/suzuha \
		--go-grpc_out=. --go-grpc_opt=module=github.com/haryoiro/suzuha \
		proto/notification/v1/notification.proto
	@echo "Proto generated: gen/consolidator/v1/ gen/notification/v1/"

# Admin API コード生成: TypeSpec → OpenAPI → ogen
api:
	cd api && pnpm exec tsp compile .
	docker compose exec agent ogen --target /app/internal/admin/api --package api --clean /app/generated/openapi.yaml
	@echo "Admin API generated: internal/admin/api/"

# 管理画面: Go API + Vite HMR を同時起動
dev-admin:
	@echo "Starting admin API (:8080) + Vite HMR (:5173)..."
	@trap 'kill 0' EXIT; \
		air -c .air.admin.toml & \
		cd web/admin && pnpm run dev & \
		wait

# Go API のみ
dev-admin-api:
	air -c .air.admin.toml

# Vite dev server のみ
dev-admin-web:
	cd web/admin && pnpm run dev

# 本番ビルド
build-admin:
	cd web/admin && pnpm run build
	CGO_ENABLED=1 go build -buildvcs=false -tags fts5 -o ./bin/suzuha-admin ./cmd/suzuha-admin
