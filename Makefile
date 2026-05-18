.PHONY: all build test test-all race lint tidy bench cover example graph versions \
        dist dist-clean dist-verify dist-fastconfd dist-fastconfctl dist-fastconfgen

# ---------------------------------------------------------------------------
# Version (overridable: make dist VERSION=v0.9.0)
# Default reads the most recent tag; "dev" when no tag exists.
# ---------------------------------------------------------------------------
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)

# ---------------------------------------------------------------------------
# Cross-compile target matrix (v0.9.0 SPEC-95)
# ---------------------------------------------------------------------------
TARGETS := \
    linux/amd64  \
    linux/arm64  \
    darwin/amd64 \
    darwin/arm64 \
    windows/amd64

BINS := fastconfd fastconfctl fastconfgen

# Allow callers to extend the matrix without editing this file:
#   make dist EXTRA_TARGETS="freebsd/amd64 linux/386"
EXTRA_TARGETS ?=
ALL_TARGETS := $(TARGETS) $(EXTRA_TARGETS)

# Module path for each binary (each lives in its own go.mod as of phase-83).
fastconfd_DIR   := cmd/fastconfd
fastconfctl_DIR := cmd/fastconfctl
fastconfgen_DIR := cmd/fastconfgen

# Common Go build flags for release binaries:
#   -trimpath        — strip $GOPATH from file paths (reproducible builds)
#   -s -w            — strip debug info and symbol table (smaller bin)
#   -X main.version  — inject the release tag at link time
DIST_LDFLAGS := -s -w -X main.version=$(VERSION)
DIST_GOFLAGS := -trimpath

# ---------------------------------------------------------------------------
# Default targets
# ---------------------------------------------------------------------------
all: build test

build:
	go build ./...

test:
	go test -race -count=1 ./...

# test-all exercises every independent sub-module from its own go.mod.
test-all: test
	cd cmd/fastconfctl       && go test -race -count=1 ./...
	cd cmd/fastconfgen       && go test -race -count=1 ./...
	cd integrations/cli/pflag && go test -race -count=1 ./...
	cd observability/metrics/prometheus && go test -race -count=1 ./...
	cd observability/otel              && go test -race -count=1 ./...
	cd policy/cue            && go test -race -count=1 ./...
	cd policy/opa            && go test -race -count=1 ./...
	cd validate/cue/cuelang  && go test -race -count=1 ./...
	cd validate/playground   && go test -race -count=1 ./...

race: test

bench:
	go test -bench=. -benchmem -run=^$$ ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

lint:
	@command -v golangci-lint >/dev/null || { echo "install golangci-lint first"; exit 1; }
	golangci-lint run ./...

tidy:
	go mod tidy

example:
	go test ./... -run Example -v

graph:
	bash tools/code-review-graph.sh

# List released root-module versions (filters out sub-module path-prefixed tags
# like policy/cue/v0.16.0; those are required by Go's module system but add
# noise to `git tag -l`). Newest first.
versions:
	@git tag -l 'v[0-9]*' --sort=-v:refname

# ---------------------------------------------------------------------------
# Cross-compile dist pipeline (v0.9.0 SPEC-95)
#
# Targets:
#   make build/<bin>-<os>-<arch>     — cross-compile a single bin/os/arch tuple
#   make dist/<bin>-<os>-<arch>      — bundle that tuple into a release archive
#   make dist-<bin>                  — archive one bin across every ALL_TARGETS
#   make dist                        — full matrix (BINS × ALL_TARGETS) + SHA256SUMS
#   make dist-clean                  — rm -rf build dist
#   make dist-verify                 — verify dist/SHA256SUMS
# ---------------------------------------------------------------------------

# Cross-compile a single bin/os/arch tuple into build/.
# Pattern stem $* looks like "fastconfd-linux-amd64".
build/%:
	@mkdir -p build
	@bin=$$(echo $* | awk -F- '{print $$1}'); \
	 os=$$(echo $*  | awk -F- '{print $$2}'); \
	 arch=$$(echo $* | awk -F- '{print $$3}'); \
	 case "$$bin" in \
	   fastconfd)   dir=cmd/fastconfd   ;; \
	   fastconfctl) dir=cmd/fastconfctl ;; \
	   fastconfgen) dir=cmd/fastconfgen ;; \
	   *) echo "unknown bin: $$bin" >&2; exit 1 ;; \
	 esac; \
	 ext=""; \
	 if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	 echo "  CROSS  $$bin ($$os/$$arch)"; \
	 cd "$$dir" && \
	   CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	     go build $(DIST_GOFLAGS) -ldflags "$(DIST_LDFLAGS)" \
	              -o "$(CURDIR)/build/$$bin-$$os-$$arch$$ext" \
	              .

# Bundle one tuple into a tar.gz (POSIX) or zip (Windows).
dist/%: build/%
	@mkdir -p dist
	@bin=$$(echo $* | awk -F- '{print $$1}'); \
	 os=$$(echo $*  | awk -F- '{print $$2}'); \
	 arch=$$(echo $* | awk -F- '{print $$3}'); \
	 ext=""; \
	 if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	 stage=$$(mktemp -d); \
	 mkdir -p "$$stage/$${bin}_$(VERSION)_$${os}_$${arch}"; \
	 cp build/$$bin-$$os-$$arch$$ext \
	    "$$stage/$${bin}_$(VERSION)_$${os}_$${arch}/$$bin$$ext"; \
	 cp LICENSE README.md \
	    "$$stage/$${bin}_$(VERSION)_$${os}_$${arch}/"; \
	 if [ "$$os" = "windows" ]; then \
	     (cd "$$stage" && zip -qr "$${bin}_$(VERSION)_$${os}_$${arch}.zip" \
	        "$${bin}_$(VERSION)_$${os}_$${arch}"); \
	     mv "$$stage/$${bin}_$(VERSION)_$${os}_$${arch}.zip" dist/; \
	 else \
	     tar -C "$$stage" -czf "dist/$${bin}_$(VERSION)_$${os}_$${arch}.tar.gz" \
	        "$${bin}_$(VERSION)_$${os}_$${arch}"; \
	 fi; \
	 rm -rf "$$stage"

# dist-<bin> convenience targets: archive one bin across every ALL_TARGETS.
define DIST_BIN_template
dist-$(1): $$(foreach t,$$(ALL_TARGETS),dist/$(1)-$$(subst /,-,$$(t)))
endef
$(foreach b,$(BINS),$(eval $(call DIST_BIN_template,$(b))))

# Main entrypoint: archive every BIN × every ALL_TARGETS, then write SHA256SUMS.
dist: $(foreach b,$(BINS),dist-$(b))
	@cd dist && \
	  if command -v sha256sum >/dev/null 2>&1; then \
	      sha256sum *.tar.gz *.zip 2>/dev/null | sort > SHA256SUMS; \
	  else \
	      shasum -a 256 *.tar.gz *.zip 2>/dev/null | sort > SHA256SUMS; \
	  fi
	@echo ""
	@echo "Dist artifacts ($(VERSION)):"
	@ls -lh dist/ | sed 's/^/  /'

dist-clean:
	rm -rf build dist

dist-verify:
	@cd dist && \
	  if command -v sha256sum >/dev/null 2>&1; then \
	      sha256sum -c SHA256SUMS; \
	  else \
	      shasum -a 256 -c SHA256SUMS; \
	  fi
