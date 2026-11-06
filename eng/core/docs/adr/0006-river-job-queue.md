# 0006: Job queue with River, migrated by goose, isolated by role

Date: 2026-11-06. Status: accepted.

Context: ARQUITECTURA.md chose River (queues on Postgres, unique jobs per
key). Datos needs a monthly job that must not run twice. Repo rules:
migrations only with goose and never edited; every table with RLS; nothing
copied from third parties without care.

Decisions:

1. **River tables in their own schema, `river`.** Not tenant data; kept
   apart from business schemas.
2. **Migrations stay goose, River's SQL is not copied.** River ships its
   schema as SQL files under MPL-2.0. Copying them into our repo would put
   MPL files inside a product we sell, a legal question for Martín, not a
   technical shortcut. Instead goose migrations 00005 and 00006 are written
   in Go and call River's migrator (`rivermigrate`, `database/sql` driver)
   inside goose's transaction, pinned to River versions 4 and 8. Two
   migrations because River 004 adds an enum value that River 006 uses, and
   Postgres only allows using it after a commit. A later River version gets
   a new goose migration; a target version is never changed.
3. **Role `tramo_jobs`** (00007): no login, no superuser, no bypass; the
   only role with privileges on `river`, with forced RLS and one policy for
   it. The queue uses its own pool that switches to it on connect, so River's
   queries cannot touch business data. `tramo_runtime` is a member.
4. **Uniqueness by natural key in the job type** (`InsertOpts` with
   `UniqueOpts`), with River's default states, which include completed jobs:
   a second enqueue of a finished key is skipped.
5. **Tenant scope in jobs** only through `jobs.TenantContext`, from the
   tenant in the arguments; business work through `db.InTenant`.
6. **Enqueue outside the business transaction for now.** The inserter
   writes in its own connection as `tramo_jobs`. Enqueuing inside a tenant
   transaction would need `tramo_app` rights on `river`; add it when a
   module needs the job to commit atomically with its data.

Consequences: the queue is in the same database and backup; the job pool is
as blind to tenant data as the system role is to business tables; River
upgrades need a new goose migration and a test run.
