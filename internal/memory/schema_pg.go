package memory

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed pg_migrations/*.sql
var pgMigrationsFS embed.FS

// migratePostgres runs all pending PostgreSQL migrations using goose.
func migratePostgres(db *sql.DB) error {
	goose.SetBaseFS(pgMigrationsFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("memory: PostgreSQLダイアレクトの設定に失敗: %w", err)
	}

	if err := goose.Up(db, "pg_migrations"); err != nil {
		return fmt.Errorf("memory: PostgreSQLマイグレーションの実行に失敗: %w", err)
	}

	return nil
}
