VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint fmt fixture run clean

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
