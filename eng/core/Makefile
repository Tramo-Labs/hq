GO ?= go
.PHONY: build test lint up down migrate gen

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...
	govulncheck ./... || true

up:
	docker compose -f deploy/docker-compose.yml up --build -d

down:
	docker compose -f deploy/docker-compose.yml down

migrate:
	goose -dir migrations postgres "$$DATABASE_URL" up

gen:
	oapi-codegen -config api/oapi.yaml api/openapi.yaml
	buf generate
