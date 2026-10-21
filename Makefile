.DEFAULT_GOAL := all
SHELL := /bin/bash
.DELETE_ON_ERROR:
.PRECIOUS: build/%

# Ignore personal workspaces and never update dependencies during checks.
export GOWORK := off
export GOFLAGS := $(GOFLAGS) -mod=readonly
VERSION ?= v1.1.0
BASE ?= latest
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
MODULES ?=
TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
EXTRA_TARGETS ?=
BINS := fastconfd fastconfctl fastconfgen
ALL_TARGETS := $(sort $(TARGETS) $(EXTRA_TARGETS))

.PHONY: all help build test race test-all test-workspace test-candidate \
        vulnerabilities api-check lint fmt-check deps-check tools-check check tidy tidy-check \
        bench bench-guard cover example module-matrix versions soak \
        dist dist-clean dist-verify $(addprefix dist-,$(BINS)) FORCE

all: build test

help:
	@cat tools/README.md

build:
	go build ./...
	(cd cmd/fastconfgen && go build .)

test:
	go test -mod=readonly -race -count=1 -timeout=5m ./...

race: test

test-all:
	bash tools/modules.sh test $(MODULES)

test-workspace:
	bash tools/modules.sh workspace $(MODULES)

test-candidate:
	VERSION=$(VERSION) bash tools/check-candidate.sh bash tools/modules.sh test $(MODULES)

vulnerabilities:
	bash tools/check-vulnerabilities.sh

api-check:
	VERSION=$(VERSION) BASE=$(BASE) bash tools/check-candidate.sh bash tools/check-api.sh

lint:
	$(GOLANGCI_LINT) run --timeout=5m ./...

fmt-check:
	bash tools/check-repo.sh fmt

deps-check:
	bash tools/check-repo.sh deps

tools-check:
	shellcheck tools/*.sh
	actionlint

check: fmt-check deps-check tidy-check
	bash tools/check-repo-test.sh
	bash tools/check-candidate-test.sh
	python3 tools/tidy-candidate-test.py
	bash tools/tag-release-test.sh
	bash tools/bench-guard-test.sh

tidy:
	VERSION=$(VERSION) bash tools/modules.sh tidy $(MODULES)

tidy-check:
	VERSION=$(VERSION) bash tools/modules.sh tidy-check $(MODULES)

bench:
	go test -run '^$$' -bench . -benchmem ./...

bench-guard:
	bash tools/bench-guard.sh

cover:
	go test -race -count=1 -timeout=5m -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

example:
	go test -count=1 -run '^Example' ./...
	go test -count=1 ./examples/...
	bash tools/modules.sh exec go -C integrations/cli/pflag run ../../../examples/pflag/main.go
	bash tools/modules.sh exec go -C providers/s3 run ../../examples/s3/main.go
	go run ./examples/fs
	bash tools/modules.sh exec go -C providers/parameterstore run ../../examples/parameterstore/main.go
	bash tools/modules.sh exec go -C providers/gcs run ../../examples/gcs/main.go
	bash tools/modules.sh exec go -C providers/azblob run ../../examples/azblob/main.go

soak:
	go test -race -count=1 -timeout=10m -run '^TestResourceSoak$$' -v ./tests/soak

module-matrix:
	@bash tools/modules.sh matrix

versions:
	@git -c versionsort.suffix=- tag -l 'v[0-9]*' --sort=-v:refname

# FORCE prevents a source or VERSION change from reusing stale binaries.
build/%: FORCE
	VERSION=$(VERSION) bash tools/dist.sh build $*

dist/%: build/%
	VERSION=$(VERSION) bash tools/dist.sh archive $*

define DIST_BIN_template
dist-$(1): $$(foreach t,$$(ALL_TARGETS),dist/$(1)-$$(subst /,-,$$(t)))
endef
$(foreach b,$(BINS),$(eval $(call DIST_BIN_template,$(b))))

dist: $(addprefix dist-,$(BINS))
	VERSION=$(VERSION) bash tools/dist.sh checksums $(foreach b,$(BINS),$(foreach t,$(ALL_TARGETS),$(b)-$(subst /,-,$(t))))

dist-verify:
	bash tools/dist.sh verify

dist-clean:
	rm -rf build dist
