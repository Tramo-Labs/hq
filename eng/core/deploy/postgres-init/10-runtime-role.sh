#!/bin/sh
# Runs once, when the Postgres data directory is first initialized (official
# image, /docker-entrypoint-initdb.d). Creates the login role the api and
# the worker use at runtime: no superuser, no RLS bypass, owns nothing.
# Migration 00004 then lets it SET ROLE to tramo_app and tramo_system only.
# The password comes from postgres.env and is passed as a psql variable, so
# it is quoted by psql and never interpolated into the SQL text.
set -eu
: "${TRAMO_RUNTIME_PASSWORD:?TRAMO_RUNTIME_PASSWORD is required in postgres.env}"
psql -v ON_ERROR_STOP=1 -v pw="$TRAMO_RUNTIME_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
create role tramo_runtime login password :'pw'
  nosuperuser nobypassrls nocreaterole nocreatedb noinherit;
SQL
