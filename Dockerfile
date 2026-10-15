# syntax=docker/dockerfile:1
#
# Test environment for codedoc. Every test (unit, lint, integration against
# real indexers and the ONNX embedder) runs in this image; the CLI, MCP
# server and daemon run on the host because they read the user's working
# trees. See docker-compose.yml and the docker-* targets of the Makefile.

# === base: Go toolchain, cgo deps, ONNX Runtime, linters, SCIP indexers ===
FROM golang:1.27.1-bookworm AS base

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gcc \
        git \
        make \
        python3 \
        python3-pip \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Node.js (scip-typescript and scip-python are npm packages). Debian's
# nodejs is too old for current scip-typescript, so use the official build.
ARG NODE_VERSION=22.23.3
RUN curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
        | tar -xJ -C /usr/local --strip-components=1 \
    && node --version && npm --version

# ONNX Runtime. onnxruntime_go v1.9.0 needs the C API of ORT 1.17.
ARG ONNX_VERSION=1.17.1
RUN curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-${ONNX_VERSION}.tgz" \
        | tar -xz -C /tmp \
    && cp -a /tmp/onnxruntime-linux-x64-${ONNX_VERSION}/lib/libonnxruntime.so* /usr/local/lib/ \
    && cp -r /tmp/onnxruntime-linux-x64-${ONNX_VERSION}/include/* /usr/local/include/ \
    && ldconfig \
    && rm -rf /tmp/onnxruntime-linux-x64-${ONNX_VERSION}
ENV CODEDOC_ONNXRUNTIME_LIB=/usr/local/lib/libonnxruntime.so

# Go tools, installed outside GOPATH so the cache volumes never shadow them.
ARG GOLANGCI_LINT_VERSION=v2.14.0
ARG SCIP_GO_VERSION=v0.2.7
RUN GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} \
    && GOBIN=/usr/local/bin go install github.com/scip-code/scip-go/cmd/scip-go@${SCIP_GO_VERSION} \
    && rm -rf /root/.cache/go-build /go/pkg/mod

# SCIP indexers for TypeScript/JavaScript and Python. scip-java is not
# installed: it needs a JDK plus Maven or Gradle, and Java SCIP integration
# tests are not part of this suite.
ARG SCIP_TYPESCRIPT_VERSION=0.4.0
ARG SCIP_PYTHON_VERSION=0.6.6
RUN npm install -g \
        "@sourcegraph/scip-typescript@${SCIP_TYPESCRIPT_VERSION}" \
        "@sourcegraph/scip-python@${SCIP_PYTHON_VERSION}" \
    && npm cache clean --force

# pnpm installs the npm packages of the real-dependency fixture (stage
# realdeps below).
ARG PNPM_VERSION=10.33.0
RUN npm install -g "pnpm@${PNPM_VERSION}" \
    && npm cache clean --force

# Tests run as an unprivileged user: tests that make files unreadable rely on
# permission checks, which root bypasses. The caches live outside the home
# directory so docker-compose can mount named volumes on them.
ARG UID=1000
ARG GID=1000
RUN groupadd -g "${GID}" codedoc \
    && useradd -m -u "${UID}" -g "${GID}" -s /bin/bash codedoc \
    && mkdir -p /cache/gomod /cache/gobuild /work \
    && chown -R codedoc:codedoc /cache /work
ENV GOMODCACHE=/cache/gomod \
    GOCACHE=/cache/gobuild \
    GOFLAGS=-buildvcs=false \
    CGO_ENABLED=1

USER codedoc
RUN git config --global user.name "codedoc test" \
    && git config --global user.email "codedoc-test@example.invalid" \
    && git config --global init.defaultBranch main

# === test: module cache warmed, source baked in for standalone runs ===
FROM base AS test

WORKDIR /opt/codedoc
COPY --chown=codedoc:codedoc go.mod go.sum ./
RUN go mod download
COPY --chown=codedoc:codedoc . .
COPY --chmod=0755 scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /work
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["make", "test", "lint"]

# === realdeps: real third-party packages for the dependency tests ===
# testdata/realdeps is installed here, at build time, so the integration
# tests read a real pnpm install and a real Go module cache without any
# network access when they run (deps spec section 11). The lockfiles pin
# every version: pnpm refuses a package.json that departs from
# pnpm-lock.yaml, and go checks every download against go.sum. The module
# cache is a directory of its own because docker-compose mounts a volume
# on /cache/gomod; the tests select it with CODEDOC_GOMODCACHE.
FROM base AS realdeps

COPY --chown=codedoc:codedoc testdata/realdeps /opt/realdeps
RUN cd /opt/realdeps/npm-app \
    && pnpm install --frozen-lockfile --ignore-scripts \
    && cd /opt/realdeps/go-app \
    && GOMODCACHE=/opt/realdeps/gomodcache GOFLAGS=-mod=readonly go mod download \
    && rm -rf /home/codedoc/.local/share/pnpm /home/codedoc/.cache/pnpm

# === model: test image plus the real-dependency fixture and the nomic
# embedding model (WITH_MODEL=1) ===
FROM test AS model

COPY --from=realdeps --chown=codedoc:codedoc /opt/realdeps /opt/realdeps
ENV CODEDOC_REALDEPS=/opt/realdeps

ARG WITH_MODEL=0
ARG MODEL_BASE=https://huggingface.co/nomic-ai/nomic-embed-text-v1.5/resolve/main
USER root
RUN mkdir -p /models \
    && if [ "${WITH_MODEL}" = "1" ]; then \
        curl -fsSL -o /models/nomic-embed-text-v1.5.onnx "${MODEL_BASE}/onnx/model.onnx" \
        && curl -fsSL -o /models/tokenizer.json "${MODEL_BASE}/tokenizer.json"; \
    fi \
    && chmod -R a+rX /models
USER codedoc
ENV CODEDOC_MODEL_DIR=/models
