.PHONY: build test lint clean install sync-skill

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X github.com/wir-drei-digital/magus-cli/internal/cli.Version=$(VERSION) \
          -X github.com/wir-drei-digital/magus-cli/internal/cli.Commit=$(COMMIT) \
          -X github.com/wir-drei-digital/magus-cli/internal/cli.BuildDate=$(BUILD_DATE)

# Keep the Go-embedded skill in sync with the plugin's canonical SKILL.md.
# Edit plugins/magus/skills/magus/SKILL.md; the embed copy is regenerated here.
internal/skill/SKILL.md: plugins/magus/skills/magus/SKILL.md
	cp $< $@

sync-skill: internal/skill/SKILL.md

build: sync-skill
	go build -ldflags "$(LDFLAGS)" -o bin/magus ./cmd/magus

test:
	go test -race ./...

lint:
	go vet ./...

clean:
	rm -rf bin/

install: build
	cp bin/magus $(GOPATH)/bin/magus 2>/dev/null || cp bin/magus /usr/local/bin/magus
