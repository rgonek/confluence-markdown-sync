BINARY     := cms
MAIN       := .
GO         := go
GOFLAGS    :=

.PHONY: build test fmt lint clean

## build: compile the cms binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN)

## test: run all tests
test:
	$(GO) test ./...

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
