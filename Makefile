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
RELEASE_TARGETS ?= $(RELEASE_OS)/$(RELEASE_ARCH)
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
GOVULNCHECK ?= golang.org/x/vuln/cmd/govulncheck@v1.5.0
COSIGN ?= cosign

.PHONY: build check clean fmt fmt-check integration-test release sbom security sign test vet vulncheck

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -buildvcs=false -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP) $(PKG)

check: fmt-check vet test integration-test

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

integration-test:
	$(GO) test -tags=integration ./tests/integration

vulncheck:
	$(GO) run $(GOVULNCHECK) ./...

security: vulncheck sbom

sbom:
	$(GO) run ./tools/release -app $(APP) -pkg $(PKG) -version $(VERSION) -commit $(COMMIT) -build-date $(BUILD_DATE) -dist $(DIST_DIR) -sbom-only

release:
	rm -rf $(DIST_DIR)
	$(GO) run ./tools/release -app $(APP) -pkg $(PKG) -version $(VERSION) -commit $(COMMIT) -build-date $(BUILD_DATE) -dist $(DIST_DIR) -targets "$(RELEASE_TARGETS)"

sign:
	@command -v $(COSIGN) >/dev/null 2>&1 || { echo "cosign is required for signing"; exit 1; }
	@for f in $(DIST_DIR)/*.tar.gz $(DIST_DIR)/*.zip $(DIST_DIR)/checksums.txt $(DIST_DIR)/manifest.json $(DIST_DIR)/sbom.spdx.json; do \
		if [ -f "$$f" ]; then \
			$(COSIGN) sign-blob --yes --output-signature "$$f.sig" "$$f"; \
		fi; \
	done
