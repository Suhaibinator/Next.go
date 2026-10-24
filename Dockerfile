# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS source
ARG TARGETOS=linux
ARG TARGETARCH
ARG APP=./examples/demo
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .

# The cache authority is independently deployable and needs no application/WASM
# build. Its only writable runtime path is the explicitly mounted data volume.
FROM source AS cache-build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/nextgo-cache ./cmd/nextgo-cache
RUN install -d -m 0700 -o 65532 -g 65532 /out/data

FROM source AS build
# Build the CLI for the builder, then cross-build its generated native server.
# The CLI compiles the browser separately with GOOS=js GOARCH=wasm.
RUN CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/nextgo ./cmd/nextgo
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH nextgo build -out /out "$APP"

# Compile the multi-module sample through its checked-in Go workspace.
FROM build AS workspace-build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH nextgo build -out /workspace-output ./examples/workspace/site
FROM scratch AS workspace-artifact
COPY --from=workspace-build /workspace-output/server /server

# Black-box deployment qualification runs the generated binary without source,
# network access, root, or a writable application filesystem. Durable cache tests
# receive only a small private tmpfs (see scripts/test-deployment-container.sh).
FROM build AS deployment-build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -tags=deployment -c -o /out/deployment.test ./tests/deployment

FROM scratch AS deployment-test
COPY --from=build /out/server /server
COPY --from=deployment-build /out/deployment.test /deployment.test
COPY --from=cache-build /out/nextgo-cache /nextgo-cache
COPY --from=workspace-build /workspace-output/server /workspace-server
COPY tests/deployment/fixtures/production.env /configuration/.env.production
USER 65532:65532
ENV NEXTGO_TEST_BINARY=/server NEXTGO_TEST_UID=65532 NEXTGO_TEST_ENV_DIR=/configuration NEXTGO_TEST_WORKSPACE_BINARY=/workspace-server NEXTGO_TEST_CACHE_BINARY=/nextgo-cache NEXTGO_TEST_CACHE_TMPDIR=/cache-tests
ENTRYPOINT ["/deployment.test"]
CMD ["-test.v", "-test.timeout=180s"]

# Export/build tools are development-only; this image never ships as runtime.
FROM otel/opentelemetry-collector-contrib:0.163.2@sha256:7f22d2a6748110ffb6a59bc0df448bccbf501cdb667d610ede4c61d3863d5849 AS collector
FROM build AS collector-build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -tags=collector -c -o /out/collector.test ./tests/collector
FROM scratch AS collector-test
COPY --from=build /out/server /server
COPY --from=collector-build /out/collector.test /collector.test
COPY --from=collector /otelcol-contrib /otelcol-contrib
USER 65532:65532
ENV NEXTGO_TEST_BINARY=/server NEXTGO_COLLECTOR_BINARY=/otelcol-contrib TMPDIR=/tmp
ENTRYPOINT ["/collector.test"]
CMD ["-test.v", "-test.timeout=45s"]

# Export/build tools are development-only; this image never ships as runtime.
FROM --platform=$BUILDPLATFORM mcr.microsoft.com/playwright:v1.64.1-noble AS static-browser-test
COPY --from=build /usr/local/go /usr/local/go
COPY --from=build /src /src
WORKDIR /src
ENV PATH="/usr/local/go/bin:${PATH}" GOTOOLCHAIN=local GOCACHE=/tmp/nextgo-go-cache
RUN npm ci --ignore-scripts
CMD ["npm", "run", "test:static:crossbrowser"]

# docker build --target artifact --output type=local,dest=./dist/linux .
FROM scratch AS artifact
COPY --from=build /out/server /server

FROM scratch AS cache-artifact
COPY --from=cache-build /out/nextgo-cache /nextgo-cache

# Non-loopback service binds require mounted TLS files and explicit -tls flags.
# Use a named /data volume for replacement/redeployment persistence; the default
# volume only establishes a writable private store alongside a read-only root.
FROM scratch AS cache-runtime
COPY --from=cache-build /out/nextgo-cache /nextgo-cache
COPY --from=cache-build --chown=65532:65532 --chmod=0700 /out/data /data
USER 65532:65532
VOLUME ["/data"]
EXPOSE 3050
ENTRYPOINT ["/nextgo-cache"]
CMD ["-addr", "127.0.0.1:3050", "-data-dir", "/data"]

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/server /server
USER 65532:65532
EXPOSE 3000
ENTRYPOINT ["/server"]
CMD ["-addr", ":3000"]
