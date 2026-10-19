BINARY ?= nemau
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet fmt tidy check install
build:
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/nemau
install:
	go install -ldflags "-X main.version=$(VERSION)" ./cmd/nemau
test:
	go test ./...
vet:
	go vet ./...
fmt:
	gofmt -w .
tidy:
	go mod tidy
check: fmt vet test
