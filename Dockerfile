# One image, amd64 and arm64: docker buildx build --platform linux/amd64,linux/arm64 .
# Build stages run on the builder's own platform and cross-compile, so no emulation is needed.

FROM --platform=$BUILDPLATFORM node:26.13.0-trixie-slim@sha256:5adad64f9a1fca2d51972caa65983634c8a774eb300d72b20f7f2a0f60368072 AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
COPY web/design/package.json ./design/
# pnpm at the version package.json pins (Node 25+ no longer bundles Corepack).
RUN npm install --global --no-fund --no-audit "pnpm@$(node -p 'require("./package.json").packageManager.split("@")[1]')" \
    && pnpm install --frozen-lockfile
COPY api/openapi.yaml /src/api/openapi.yaml
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -tags embedweb -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

# Static distroless: no shell, no package manager, uid 65532. Runs on a read-only filesystem;
# the binary writes nothing.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
CMD ["serve"]
