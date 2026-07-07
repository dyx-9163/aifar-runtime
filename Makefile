APP ?= aifar-runtime
PKG ?= ./cmd/aifar-runtime
BIN_DIR ?= bin
GO ?= go

.PHONY: build check clean fmt fmt-check test vet

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BIN_DIR)/$(APP) $(PKG)

check: fmt-check vet test

clean:
	rm -rf $(BIN_DIR)

fmt:
	$(GO) fmt ./...

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt required:"; \
		echo "$$files"; \
		exit 1; \
	fi

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
