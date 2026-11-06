-- +goose Up
-- The person's name, as they want it shown (B). Null until they set one;
-- the web shows the email then. Personal data, no format check beyond its
-- length (identity trims it and refuses control characters).
alter table users add column display_name text
  check (display_name is null or char_length(display_name) between 1 and 120);

-- tramo_app reads users only through column grants (00002): the name is
-- visible where the email already is, to members of the same tenant.
grant select (display_name) on users to tramo_app;

-- +goose Down
revoke select (display_name) on users from tramo_app;
alter table users drop column display_name;
