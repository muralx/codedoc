BINARY := codedoc
TAGS := sqlite_fts5
# COVERAGE_MIN is the least total statement coverage of internal/ that
# make test accepts (spec section 16).
COVERAGE_MIN := 90

# MODEL_DIR receives the nomic-embed-text-v1.5 model from make
# download-model; point CODEDOC_MODEL_DIR at it to run the model tests.
MODEL_DIR := models
MODEL_BASE := https://huggingface.co/nomic-ai/nomic-embed-text-v1.5/resolve/main
COMPOSE := docker compose
# VERSION names the build; the daemon tells clients of another build apart.
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)

.PHONY: build install test integration lint clean download-model \
	docker-build docker-test docker-lint docker-integration docker-all docker-shell

LDFLAGS := -X main.version=$(VERSION)

build:
	CGO_ENABLED=1 go build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/codedoc

# install puts codedoc in $(go env GOPATH)/bin, where
# deploy/systemd/codedoc.service expects it.
install:
	CGO_ENABLED=1 go install -tags "$(TAGS)" -ldflags "$(LDFLAGS)" ./cmd/codedoc

test:
	CGO_ENABLED=1 go test -tags "$(TAGS)" -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	{ head -1 coverage.out; grep '/internal/' coverage.out; } > coverage.internal.out
	go tool cover -func=coverage.internal.out | awk -v min=$(COVERAGE_MIN) '/^total:/ { pct = $$NF; sub("%", "", pct); \
		printf "internal/ coverage %s%% (minimum %s%%)\n", pct, min; if (pct + 0 < min) exit 1 }'

# integration runs every test plus those tagged integration, which need the
# real toolchain (scip indexers, ONNX Runtime); use make docker-integration.
integration:
	CGO_ENABLED=1 go test -tags "$(TAGS) integration" ./...

# gofmt skips .claude/, where agent worktrees keep copies of the tree;
# go vet and golangci-lint never enter a directory starting with a dot.
lint:
	gofmt -l $$(find . -name '*.go' -not -path './.claude/*') | tee /dev/stderr | test -z "$$(cat)"
	go vet -tags "$(TAGS) integration" ./...
	golangci-lint run --build-tags "$(TAGS),integration"

download-model:
	mkdir -p $(MODEL_DIR)
	curl -fL -o $(MODEL_DIR)/nomic-embed-text-v1.5.onnx "$(MODEL_BASE)/onnx/model.onnx"
	curl -fL -o $(MODEL_DIR)/tokenizer.json "$(MODEL_BASE)/tokenizer.json"

docker-build:
	$(COMPOSE) build test integration

docker-test:
	$(COMPOSE) run --rm test

docker-lint:
	$(COMPOSE) run --rm lint

docker-integration:
	$(COMPOSE) run --rm integration

docker-all: docker-test docker-lint docker-integration

docker-shell:
	$(COMPOSE) run --rm shell

clean:
	rm -f $(BINARY) coverage.out coverage.internal.out
