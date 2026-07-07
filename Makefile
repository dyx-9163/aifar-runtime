APP ?= aifar-runtime
PKG ?= ./cmd/aifar-runtime
BIN_DIR ?= bin
DIST_DIR ?= dist
GO ?= go
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
RELEASE_OS ?= linux
RELEASE_ARCH ?= amd64
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
RELEASE_NAME := $(APP)-$(VERSION)-$(RELEASE_OS)-$(RELEASE_ARCH)

.PHONY: build check clean fmt fmt-check release test vet

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP) $(PKG)

check: fmt-check vet test

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

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

release:
	rm -rf $(DIST_DIR)/$(RELEASE_NAME)
	mkdir -p $(DIST_DIR)/$(RELEASE_NAME)/deploy/systemd $(DIST_DIR)/$(RELEASE_NAME)/docs
	CGO_ENABLED=0 GOOS=$(RELEASE_OS) GOARCH=$(RELEASE_ARCH) $(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(RELEASE_NAME)/$(APP) $(PKG)
	cp README.md SKILL.md $(DIST_DIR)/$(RELEASE_NAME)/
	cp deploy/systemd/* $(DIST_DIR)/$(RELEASE_NAME)/deploy/systemd/
	cp docs/*.md $(DIST_DIR)/$(RELEASE_NAME)/docs/
	tar -czf $(DIST_DIR)/$(RELEASE_NAME).tar.gz -C $(DIST_DIR) $(RELEASE_NAME)
