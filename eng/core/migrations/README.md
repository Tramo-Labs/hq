# migrations

goose migrations of the core, embedded in the binary (`embed.go`) and
applied by `api migrate` (production, privileged role) or on start in dev.
River's own schema is applied from Go migrations here too (`river.go`, ADR
0006).

## Numbering: one sequence

A new migration takes **the next number above the highest one in main**,
whoever writes it. There are no ranges per team any more (the old "backend
00002 to 00099, datos 00100 to 00199" rule is gone): goose refuses to start
a database that already applied a higher version than a migration it has
not run, so a branch adding 00008 when main is at 00109 cannot be deployed
("detected 1 missing (out-of-order) migration lower than database version").

If two branches take the same next number, the one that lands second
renumbers before it merges. Renaming is fine only while the migration is
not in main; once in main a migration is never edited or renamed.

`TestMigrationsAboveMain` (`order_test.go`) enforces it: every migration
not in `origin/main` (or `MIGRATIONS_BASE`) must be above main's highest,
and no two share a version. Locally without `origin/main` it skips; in CI it
fails, and the core workflow checks out with `fetch-depth: 0` so the ref is
there. `TestDatabaseFromMainUpgrades` (`internal/platform/db`) runs the
upgrade itself: an empty database migrated with main's migrations only,
then with everything, as a deploy would. Mutation checked: giving 00112
back its old number 00010 fails both, the second with goose's own "missing
(out-of-order) migration lower than database version (109)".

## Rules every migration follows

- `tenant_id` and row level security on every business table, `force row
  level security` included; grants only to the roles that need them
  (platform README).
- Never edited after it is in main. A change is a new migration.
- `-- +goose Down` that undoes it.

## Dev data from an earlier branch

A `make dev` data directory created from a branch that still had 00008,
00009 and 00010 (now 00110, 00111 and 00112) has those versions recorded
and will not migrate. It holds synthetic data only: delete it and run
`make dev` again (`rm -rf ~/.local/share/tramo-dev`, or your
`DEV_DATA_DIR`).
