# One image, three commands: api, worker, arca-gateway (chosen at run time).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/arca-gateway ./cmd/arca-gateway

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
# The spec and Swagger UI live next to the binaries, not on top of /app/api.
# Migrations are embedded in the binaries; api applies them at startup.
COPY --from=build /src/api/openapi.yaml /src/api/index.html /app/docs/
ENV DOCS_DIR=/app/docs
USER nonroot
ENTRYPOINT ["/app/api"]
