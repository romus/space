VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Where `make install` puts the binary: $(PREFIX)/bin. The default needs no
# sudo and stays out of Homebrew's way. For a system-wide install:
#   sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

.PHONY: build install uninstall test lint fmt fixture run clean

install: build ## install space into $(PREFIX)/bin
	install -d "$(BINDIR)"
	install -m 0755 space "$(BINDIR)/space"
	@echo "installed $(BINDIR)/space"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: $(BINDIR) is not on your PATH";; esac

uninstall: ## remove space from $(PREFIX)/bin; settings and logs are kept
	rm -f "$(BINDIR)/space"
	@echo "removed $(BINDIR)/space"
	@echo "kept: ~/.config/space and ~/.local/state/space (delete them by hand if you want)"


build: ## build ./space for this Mac
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o space ./cmd/space

test: ## run every test
	go test ./...

lint: ## vet and check formatting
	go vet -composites=false ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

fixture: ## build a throwaway home in /tmp/space-home
	hack/mkfixture.sh /tmp/space-home

run: build fixture ## try space on the throwaway home, never on real data
	SPACE_HOME=/tmp/space-home ./space

clean:
	rm -f space
	rm -rf dist
