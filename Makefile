.PHONY: dev-admin dev-admin-api dev-admin-web build-admin

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
	CGO_ENABLED=1 go build -tags fts5 -o ./bin/suzuha-admin ./cmd/suzuha-admin
