FROM golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0 AS go-build
WORKDIR /src
ENV GOTOOLCHAIN=local GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.org CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
COPY schemas ./schemas
RUN go build -trimpath -o /out/yuanfa ./backend/cmd/yuanfa \
 && go build -trimpath -o /out/yuanfa-backup ./backend/cmd/yuanfa-backup

FROM node:22.23.4-bookworm-slim@sha256:5ec23b7c622b00547753bc6ffd78085dd868a68aeb41c8e0a1418f033e13d96d AS frontend
WORKDIR /src
ENV ASTRO_TELEMETRY_DISABLED=1
COPY package.json package-lock.json ./
RUN test "$(node --version)" = v22.23.4 && test "$(npm --version)" = 10.9.9 \
 && npm ci --no-audit --no-fund
COPY src ./src
COPY public ./public
COPY schemas ./schemas
COPY admin ./admin
COPY scripts ./scripts
COPY licenses ./licenses
COPY tsconfig.json astro.config.mjs ./
RUN npm run site:config && npm run build:admin \
 && node scripts/package-runtime.mjs --root /src --out /runtime

FROM node:22.23.4-bookworm-slim@sha256:5ec23b7c622b00547753bc6ffd78085dd868a68aeb41c8e0a1418f033e13d96d
# prlimit enforces the existing publication worker limits; curl probes Go directly.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl util-linux \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir /app /data && chown 1000:1000 /data
WORKDIR /app
COPY --from=frontend /runtime/ /app/
COPY --from=go-build /out/yuanfa /out/yuanfa-backup /app/
ENV DATA_DIR=/data HTTP_ADDR=0.0.0.0:8080 \
 TEMPLATE_DIR=/app/template ADMIN_STATIC_DIR=/app/admin SITE_CONFIG_PATH=/app/site-config.json \
 HOME=/data TMPDIR=/data/tmp ASTRO_TELEMETRY_DISABLED=1
USER 1000:1000
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
 CMD curl --fail --silent http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["/app/yuanfa"]
CMD ["serve"]
