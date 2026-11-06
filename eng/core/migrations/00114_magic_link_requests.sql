-- +goose Up
-- Pending magic link requests (D-17). The request writes a row here, the
-- same for any email, and enqueues a job that carries only this row's id:
-- River stores job arguments in clear, and the house rule keeps personal
-- data out of them. The job takes the row (deletes it) in the same
-- transaction that looks the email up and stores the link. Rows left by an
-- enqueue that failed are purged by the daily maintenance.
--
-- Like invites and magic_links, only identity touches it, through
-- tramo_system; tramo_app gets nothing.
create table magic_link_requests (
  id          uuid primary key default gen_random_uuid(),
  email       text not null,
  tenant_id   uuid references tenants(id) on delete cascade,
  request_id  text,
  ip          inet,
  created_at  timestamptz not null default now()
);
create index magic_link_requests_created on magic_link_requests (created_at);

revoke all on magic_link_requests from tramo_app;
grant select, insert, update, delete on magic_link_requests to tramo_system;
alter table magic_link_requests enable row level security;
create policy system_all on magic_link_requests to tramo_system using (true) with check (true);
alter table magic_link_requests force row level security;

-- +goose Down
drop table magic_link_requests;
