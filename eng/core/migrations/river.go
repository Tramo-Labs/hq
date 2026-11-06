package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

// RiverSchema holds River's tables, apart from the business schemas.
const RiverSchema = "river"

// Go lists the migrations written in Go. They run in goose order with the
// SQL files of this directory.
//
// 00005 and 00006 create River's job queue tables (ADR 0006) by calling
// River's own migrator, pinned to fixed River versions, inside goose's
// transaction. River's SQL is not copied into this repo (it is MPL-2.0).
// They are two migrations on purpose: River 004 adds a value to an enum and
// River 006 uses it; Postgres only allows that after a commit, and goose
// commits between migrations.
//
// Never change a target version here: a later River version gets a new
// goose migration.
var Go = []*goose.Migration{
	riverMigration(5, 4),
	riverMigration(6, 8),
}

func riverMigration(gooseVersion int64, riverTarget int) *goose.Migration {
	up := &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "create schema if not exists "+RiverSchema); err != nil {
			return err
		}
		m, err := rivermigrate.New(riverdatabasesql.New(nil), &rivermigrate.Config{Schema: RiverSchema})
		if err != nil {
			return err
		}
		if _, err := m.MigrateTx(ctx, tx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{TargetVersion: riverTarget}); err != nil {
			return fmt.Errorf("river migrate to %d: %w", riverTarget, err)
		}
		return nil
	}}
	return goose.NewGoMigration(gooseVersion, up, nil)
}
