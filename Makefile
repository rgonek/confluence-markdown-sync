BINARY     := conf
MAIN       := ./cmd/conf
GO         := go
GOFLAGS    :=

.PHONY: build install test test-unit test-e2e release-check coverage-check fmt fmt-check lint clean

## build: compile the conf binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY) $(MAIN)

## install: install the binary to $GOPATH/bin
install:
	$(GO) install $(MAIN)

## test: run the default local test suite
test: test-unit

## test-unit: run all non-E2E tests
test-unit:
	$(GO) test ./...

## coverage-check: enforce package coverage minimums
coverage-check:
	$(GO) run ./tools/coveragecheck

## test-e2e: run all end-to-end tests (requires CONF_E2E_DOMAIN, CONF_E2E_EMAIL, CONF_E2E_API_TOKEN, CONF_E2E_PRIMARY_SPACE_KEY, CONF_E2E_SECONDARY_SPACE_KEY)
test-e2e: build
	$(GO) test -v -tags=e2e ./cmd -run '^TestWorkflow_'

## release-check: run the release gate, including live sandbox E2E coverage
release-check: fmt-check lint test-unit test-e2e


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
