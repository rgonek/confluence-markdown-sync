BINARY     := cms
MAIN       := .
GO         := go
GOFLAGS    :=

.PHONY: build test fmt lint clean

## build: compile the cms binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN)

## test: run all unit tests
test:
	$(GO) test ./...

## test-e2e: run all end-to-end tests (requires credentials)
test-e2e: build
	$(GO) test -v -tags=e2e ./cmd -run TestWorkflow


## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## lint: vet the code (no external linter required)
lint:
	$(GO) vet ./...

## clean: remove build artifacts
clean:
	$(GO) clean
	@if exist $(BINARY) del /f $(BINARY)
	@if exist $(BINARY).exe del /f $(BINARY).exe
