BINARY     := conf
MAIN       := ./cmd/conf
GO         := go
GOFLAGS    :=

.PHONY: build install test fmt lint clean

## build: compile the conf binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN)

## install: install the binary to $GOPATH/bin
install:
	$(GO) install $(MAIN)

## test: run all unit tests
test:
	$(GO) test ./...

## test-e2e: run all end-to-end tests (requires credentials)
test-e2e: build
	$(GO) test -v -tags=e2e ./cmd -run TestWorkflow


## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## lint: run golangci-lint (falls back to go vet)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		$(GO) vet ./...; \
	fi

## clean: remove build artifacts
clean:
	$(GO) clean
	@if exist $(BINARY) del /f $(BINARY)
	@if exist $(BINARY).exe del /f $(BINARY).exe
