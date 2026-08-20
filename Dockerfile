# LadyM enterprise-edition image: Postgres-only binaries (no sqlite) on
# distroless. Two binaries, three roles:
#   /ladym        — api (`serve --http`) and worker (`worker`) roles; does NOT
#                   embed the Vue console
#   /ladymconsole — console role (full /api data-plane + SPA at /)
#
#   docker build -t ladym-enterprise:local .
#
# Reference deployments: docker-compose.enterprise.yml (three-tier: nginx
# gateway + api x2 + console + worker + PG, only the gateway exposed) and
# docker-compose.dev.yml (single-node dev group) — see docs/deployment.md.
# The console role uses the same image with an entrypoint
# override: `entrypoint: ["/ladymconsole"]` (docker compose `command:` only
# replaces args to /ladym, not the binary).

# ---------------------------------------------------------------------------
# console stage — rebuild the embedded management console inside the image
# (reproducible; the committed console/dist copy is not used here). Only the
# ladymconsole binary embeds it.
# ---------------------------------------------------------------------------
FROM node:24-alpine AS console
WORKDIR /src/console
COPY console/package.json console/package-lock.json ./
RUN npm ci
COPY console/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# build stage
# ---------------------------------------------------------------------------
FROM golang:1.26 AS build
WORKDIR /src

# Module layer cached separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Fresh console build from the node stage, replacing the committed copy.
RUN rm -rf console/dist
COPY --from=console /src/console/dist console/dist
RUN CGO_ENABLED=0 go build -tags enterprise -trimpath -ldflags "-s -w" \
      -o /out/ladym ./cmd/ladym
RUN CGO_ENABLED=0 go build -tags enterprise -trimpath -ldflags "-s -w" \
      -o /out/ladymconsole ./cmd/ladymconsole

# Enterprise gates baked into the image build (same checks as
# `make verify-enterprise`): ladym must carry pgx, must NOT carry
# modernc.org/sqlite, and must NOT depend on the console package (no Vue
# embed); ladymconsole must carry the console. A violation fails the build.
RUN ! go version -m /out/ladym | grep -q modernc.org/sqlite \
    && go version -m /out/ladym | grep -q pgx \
    && ! go list -tags enterprise -deps ./cmd/ladym | grep -q 'ProjAnvil/LadyM/console$' \
    && go list -tags enterprise -deps ./cmd/ladymconsole | grep -q 'ProjAnvil/LadyM/console$'

# ---------------------------------------------------------------------------
# runtime stage — distroless static, non-root
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ladym /ladym
COPY --from=build /out/ladymconsole /ladymconsole
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/ladym"]
CMD ["serve", "--http", ":8080"]
