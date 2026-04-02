VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/gschlager/silo/internal/cli.Version=$(VERSION)

.PHONY: build install clean vet

build:
	go build -ldflags="$(LDFLAGS)" -o silo ./cmd/silo

install:
	go install -ldflags="$(LDFLAGS)" ./cmd/silo

vet:
	go vet ./...

clean:
	rm -f silo
