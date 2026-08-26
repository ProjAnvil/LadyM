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

# Build-tag flavor. Default "enterprise" (Postgres-only binaries).
# "enterprise,fulldict" additionally embeds the CJK dictionary (~+31MB) for
# air-gapped deployments:
#   docker build --build-arg BUILD_TAGS=enterprise,fulldict -t ladym-enterprise:full .
ARG BUILD_TAGS=enterprise

# Module layer cached separately from source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Fresh console build from the node stage, replacing the committed copy.
RUN rm -rf console/dist
COPY --from=console /src/console/dist console/dist
RUN CGO_ENABLED=0 go build -tags "${BUILD_TAGS}" -trimpath -ldflags "-s -w" \
      -o /out/ladym ./cmd/ladym
RUN CGO_ENABLED=0 go build -tags "${BUILD_TAGS}" -trimpath -ldflags "-s -w" \
      -o /out/ladymconsole ./cmd/ladymconsole

# Enterprise gates baked into the image build (same checks as
# `make verify-enterprise`): ladym must carry pgx, must NOT carry
# modernc.org/sqlite, and must NOT depend on the console package (no Vue
# embed); ladymconsole must carry the console. A violation fails the build.
RUN ! go version -m /out/ladym | grep -q modernc.org/sqlite \
    && go version -m /out/ladym | grep -q pgx \
    && ! go list -tags "${BUILD_TAGS}" -deps ./cmd/ladym | grep -q 'ProjAnvil/LadyM/console$' \
    && go list -tags "${BUILD_TAGS}" -deps ./cmd/ladymconsole | grep -q 'ProjAnvil/LadyM/console$'

# ---------------------------------------------------------------------------
# dict data stage — downloads + sha256-verifies the CJK dictionary and
# writes the manifest. Only built for the `dict` target (BuildKit skips it
# for plain builds, which stay network-free). Keep this script in sync with
# Dockerfile.dict; pins must match storage/cjk_dict.go (gse v1.0.2).
# ---------------------------------------------------------------------------
FROM alpine:3.20 AS dictdata
ARG DICT_VARIANT=zh
RUN apk add --no-cache curl ca-certificates
COPY dict/ /src/dict/
RUN set -eux; \
    case "$DICT_VARIANT" in \
      zh)   files="s_1.txt t_1.txt" ;; \
      zh_s) files="s_1.txt" ;; \
      zh_t) files="t_1.txt" ;; \
      jp)   files="jp.txt" ;; \
      *) echo "unknown DICT_VARIANT: $DICT_VARIANT (want zh|zh_s|zh_t|jp)" >&2; exit 1 ;; \
    esac; \
    mkdir -p /opt/ladym/dict; \
    cd /opt/ladym/dict; \
    for f in $files; do \
      case "$f" in \
        s_1.txt) rel=data/dict/zh/s_1.txt; sum=2b3063ec552327520bee3c0c5819d6e131ab3db50a60b94641ec90f611c24bcd ;; \
        t_1.txt) rel=data/dict/zh/t_1.txt; sum=2c84cef353d2daac62cc62bbeabab6b6a8866cfee8f9f88901e00ed66ed208c6 ;; \
        jp.txt)  rel=data/dict/jp/dict.txt; sum=b7de28abfda94ed009f1e7fc67333393a449825ca653a7eb101fee61dfeda4ed ;; \
      esac; \
      if [ -f "/src/dict/$f" ]; then \
        cp "/src/dict/$f" "$f"; \
      else \
        curl -fsSL --retry 3 "https://cdn.jsdelivr.net/gh/go-ego/gse@v1.0.2/$rel" -o "$f" \
          || curl -fsSL --retry 3 "https://raw.githubusercontent.com/go-ego/gse/v1.0.2/$rel" -o "$f"; \
      fi; \
      echo "$sum  $f" | sha256sum -c -; \
    done; \
    printf '{"version":"gse-v1.0.2","variant":"%s"}\n' "$DICT_VARIANT" > manifest.json

# ---------------------------------------------------------------------------
# runtime stage — distroless static, non-root
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS bin
COPY --from=build /out/ladym /ladym
COPY --from=build /out/ladymconsole /ladymconsole
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/ladym"]
CMD ["serve", "--http", ":8080"]

# dict target — the binary image plus the dictionary baked in at
# /opt/ladym/dict (LADYM_DICT_DIR). Build with:
#   docker build --target dict --build-arg DICT_VARIANT=zh -t ladym-enterprise:dict .
FROM bin AS dict
COPY --from=dictdata /opt/ladym/dict /opt/ladym/dict
ENV LADYM_DICT_DIR=/opt/ladym/dict

# default target (last stage) — plain dictionary-less image, so a bare
# `docker build .` keeps today's behavior (including fully offline builds;
# the dictdata stage above is not part of this target's graph).
FROM bin
