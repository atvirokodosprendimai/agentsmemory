# syntax=docker/dockerfile:1

# ---- build stage ----
# The builder version tracks go.mod (go 1.25); 1.26 is backward compatible.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Download modules first so this layer caches when only source changes.
COPY go.mod go.sum ./
RUN go mod download

# Build a fully static, CGO-free binary. The pure-Go SQLite driver (glebarez)
# means no cgo is needed; the goose migrations and the dashboard's static
# assets are embedded, so the resulting binary is entirely self-contained.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/agentsmemory ./cmd/server

# ---- runtime stage ----
FROM alpine:3.20
# ca-certificates for outbound TLS (OAuth providers); tzdata for correct
# monthly-usage period boundaries; a non-root user owning a writable data dir.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 app \
 && mkdir -p /data && chown app:app /data

COPY --from=build /out/agentsmemory /usr/local/bin/agentsmemory

USER app
WORKDIR /data
EXPOSE 8080
# SQLite lives on a volume so a project's data survives container restarts.
VOLUME ["/data"]

# Secrets/config come from the environment (see .env.example): OAUTH_ISSUER,
# OAUTH_SECRET_KEY, AGENTSMEMORY_SESSION_KEY, optional social-login keys.
# The entrypoint is the bare binary so `docker run <img> mcp search "q"` reaches
# the read-only CLI; the default command serves explicitly.
ENTRYPOINT ["agentsmemory"]
CMD ["serve", "--addr", ":8080", "--db", "/data/agentsmemory.db"]
