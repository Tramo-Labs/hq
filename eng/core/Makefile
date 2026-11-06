GO ?= go
# Code generators and checkers, pinned. `make tools` installs them into
# ./bin (ignored by git) so local runs and CI use the same versions.
OAPI_CODEGEN_VERSION := v2.8.2
BUF_VERSION          := v1.75.0
PROTOC_GEN_GO        := v1.36.12
PROTOC_GEN_GO_GRPC   := v1.6.2
GOOSE_VERSION        := v3.29.0
SQLC_VERSION         := v1.31.1
GOSEC_VERSION        := v2.30.0
GOVULNCHECK_VERSION  := v1.9.0

BIN := $(CURDIR)/bin
export PATH := $(BIN):$(PATH)

.PHONY: build test test-short lint sec tools tools-sec gen gen-check dev up down migrate

build:
	$(GO) build ./...

# Database tests run on an embedded Postgres 17 (ports 54329-54338).
test:
	$(GO) test -race ./...

# Without the database tests.
test-short:
	$(GO) test -race -short ./...

lint:
	test -z "$$(gofmt -l . | grep -v '^gen/')"
	$(GO) vet ./...
	buf lint

# Security: static analysis and known vulnerabilities. Fails on findings.
sec:
	gosec -quiet -exclude-generated -exclude-dir=gen ./...
	govulncheck ./...

tools:
	GOBIN=$(BIN) $(GO) install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	GOBIN=$(BIN) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOBIN=$(BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO)
	GOBIN=$(BIN) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC)
	GOBIN=$(BIN) $(GO) install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	GOBIN=$(BIN) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# Security tools are separate: some sandboxes cannot fetch them.
tools-sec:
	GOBIN=$(BIN) $(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# Regenerates gen/ from api/openapi.yaml and proto/. Generated code is
# committed so `go build` works without the tools.
gen:
	mkdir -p gen/api
	oapi-codegen -config api/oapi.yaml api/openapi.yaml
	buf generate
	sqlc generate

# Fails when gen/ is stale (run in CI).
gen-check: gen
	git diff --exit-code -- gen/ internal/*/store/

# Whole core locally without Docker: embedded Postgres, migrations, seed,
# api on :8080, worker, gateway on the ARCA simulator. Dev only (refuses
# APP_ENV other than dev). APP_ENV is deliberately not set here, so a shell
# with APP_ENV=prod is refused instead of overridden.
dev:
	$(GO) run ./cmd/dev

up:
	docker compose -f deploy/docker-compose.yml up --build -d

down:
	docker compose -f deploy/docker-compose.yml down

migrate:
	goose -dir migrations postgres "$$DATABASE_URL" up
