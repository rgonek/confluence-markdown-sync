BINARY     := conf
MAIN       := ./cmd/conf
GO         := go
GOFLAGS    :=

.PHONY: build install test coverage-check fmt fmt-check lint clean

## build: compile the conf binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN)

## install: install the binary to $GOPATH/bin
install:
	$(GO) install $(MAIN)

## test: run all unit tests
test:
	$(GO) test ./...

## coverage-check: enforce package coverage minimums
coverage-check:
	$(GO) run ./tools/coveragecheck

## test-e2e: run all end-to-end tests (requires credentials)
test-e2e: build
	$(GO) test -v -tags=e2e ./cmd -run TestWorkflow


## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## fmt-check: fail if go files are unformatted
fmt-check:
	$(GO) run ./tools/gofmtcheck

## lint: run static checks
lint:
	$(GO) vet ./...

## clean: remove build artifacts
clean:
	$(GO) clean
	$(GO) clean -cache
