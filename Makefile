CURRENT_VERSION_MAJOR = 0
CURRENT_VERSION_MINOR = 0
CURRENT_VERSION_PATCH = 1

# Keep the three version assignments above in this format for release tooling.

.PHONY: build core web lint run clean digest version

GO ?= go
NPM ?= npm

SRC_DIR = ./cmd/proxygate
WEB_DIR = ./web/src
DIST_DIR = ./build/dist

GOOS ?= $(shell $(GO) env GOOS)
GOARCH ?= $(shell $(GO) env GOARCH)
VERSION_PRE_RELEASE ?= dev
BUILD_CHANNEL ?= local
BUILD_TOOLCHAIN ?= $(GOOS)_$(GOARCH)
TIMESTAMP ?= $(shell date +%s)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)

BINARY = proxygate
ifeq ($(GOOS),windows)
	BINARY := $(BINARY).exe
endif

BUILD_FLAGS = -s -w \
	-X proxygate/internal/buildinfo.VersionMajor=$(CURRENT_VERSION_MAJOR) \
	-X proxygate/internal/buildinfo.VersionMinor=$(CURRENT_VERSION_MINOR) \
	-X proxygate/internal/buildinfo.VersionPatch=$(CURRENT_VERSION_PATCH) \
	-X proxygate/internal/buildinfo.VersionPreRelease=$(VERSION_PRE_RELEASE) \
	-X proxygate/internal/buildinfo.BuildToolchain=$(BUILD_TOOLCHAIN) \
	-X proxygate/internal/buildinfo.BuildChannel=$(BUILD_CHANNEL) \
	-X proxygate/internal/buildinfo.BuildTimestamp=$(TIMESTAMP) \
	-X proxygate/internal/buildinfo.BuildCommit=$(COMMIT)
BUILD_ARGS = -trimpath -buildvcs=false

build: web core

web:
	cd $(WEB_DIR) && $(NPM) ci && $(NPM) run build

core:
	@test -f ./web/dist/index.html || (printf '%s\n' 'web/dist is missing; run make web first' && exit 1)
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) GOMIPS=$(GOMIPS) \
		$(GO) build $(BUILD_ARGS) -ldflags="$(BUILD_FLAGS)" -o $(DIST_DIR)/$(BINARY) $(SRC_DIR)

lint:
	cd $(WEB_DIR) && $(NPM) run lint

run: web
	CGO_ENABLED=0 $(GO) run $(BUILD_ARGS) -ldflags="$(BUILD_FLAGS)" $(SRC_DIR) -config ./config.json

digest:
	@openssl dgst -sha256 $(DIST_DIR)/$(BINARY)

version:
	@printf '%s.%s.%s' $(CURRENT_VERSION_MAJOR) $(CURRENT_VERSION_MINOR) $(CURRENT_VERSION_PATCH); \
	if [ -n "$(VERSION_PRE_RELEASE)" ]; then printf -- '-%s' "$(VERSION_PRE_RELEASE)"; fi; \
	printf '\n'

clean:
	rm -rf ./build/dist ./web/dist
