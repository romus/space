# The version is the git tag without its leading v: tag v1.2.3 builds "space 1.2.3".
VERSION ?= $(shell (git describe --tags --always --dirty 2>/dev/null || echo dev) | sed 's/^v//')
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Where `make install` puts the binary: $(PREFIX)/bin. The default needs no
# sudo and stays out of Homebrew's way. For a system-wide install:
#   sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

# The release archive Homebrew installs: one universal binary, so the formula
# needs a single checksum.
DIST    := dist
ARCHIVE := space-$(VERSION)-macos-universal.tar.gz

# What space keeps for the user, at the same paths the program works out.
CONFIG_DIR = $${XDG_CONFIG_HOME:-$$HOME/.config}/space
STATE_DIR  = $${XDG_STATE_HOME:-$$HOME/.local/state}/space

.PHONY: build install uninstall uninstall-all dist formula test race lint fmt fixture run clean

install: build ## install space into $(PREFIX)/bin
	install -d "$(BINDIR)"
	install -m 0755 space "$(BINDIR)/space"
	@echo "installed $(BINDIR)/space"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "note: $(BINDIR) is not on your PATH";; esac

uninstall: ## remove space from $(PREFIX)/bin; settings and logs are kept
	rm -f "$(BINDIR)/space"
	@echo "removed $(BINDIR)/space"
	@echo "kept: settings and logs. To remove them too: make uninstall-all"

uninstall-all: ## remove space and everything it keeps: settings, state, logs
	rm -f "$(BINDIR)/space"
	rm -rf "$(CONFIG_DIR)" "$(STATE_DIR)"
	@echo "removed $(BINDIR)/space"
	@echo "removed $(CONFIG_DIR)"
	@echo "removed $(STATE_DIR)"


build: ## build ./space for this Mac
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o space ./cmd/space

dist: ## build the universal binary and archive it for Homebrew in dist/
	rm -rf $(DIST)
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/space-arm64 ./cmd/space
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/space-amd64 ./cmd/space
	lipo -create -output $(DIST)/space $(DIST)/space-arm64 $(DIST)/space-amd64
	rm $(DIST)/space-arm64 $(DIST)/space-amd64
	@# The Go linker signs only the arm64 slice. Sign both, ad hoc, so the
	@# whole file passes `codesign --verify`.
	codesign --force --sign - --identifier dev.romus.space $(DIST)/space
	cp LICENSE $(DIST)/LICENSE
	COPYFILE_DISABLE=1 tar -C $(DIST) -czf $(DIST)/$(ARCHIVE) space LICENSE
	@echo "$(DIST)/$(ARCHIVE)"
	@echo "sha256: $$(shasum -a 256 $(DIST)/$(ARCHIVE) | awk '{print $$1}')"

# FORMULA_URL points the formula at another download, such as a file:// path
# to the archive in dist/ for trying it from a local tap.
formula: ## render the Homebrew formula for the archive in dist/
	scripts/render_formula.sh \
		--version "$(VERSION)" \
		--sha256 "$$(shasum -a 256 $(DIST)/$(ARCHIVE) | awk '{print $$1}')" \
		$(if $(FORMULA_URL),--url "$(FORMULA_URL)") \
		--output $(DIST)/space.rb

test: ## run every test
	go test ./...

race: ## run the tests of everything that runs side by side under the race detector
	go test -race ./internal/engine ./internal/ui/screens

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
	rm -rf $(DIST)
