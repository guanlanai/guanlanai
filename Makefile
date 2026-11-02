APP=guanlanai
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X github.com/guanlanai/guanlanai/internal/version.Version=$(VERSION) \
	-X github.com/guanlanai/guanlanai/internal/version.Commit=$(COMMIT)

.PHONY: fmt fmt-check vet test test-race lint build run docker docker-multi

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

lint: fmt-check vet

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/bot

run:
	@set -a; if [ -f .env ]; then . ./.env; fi; set +a; go run ./cmd/bot

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(APP):$(VERSION) .

docker-multi:
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		-t $(APP):$(VERSION) .
