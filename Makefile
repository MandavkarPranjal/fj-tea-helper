BINARY := fj-tea-helper
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

.PHONY: all build install test check fmt vet clean

all: check build

build:
	go build -o $(BINARY) .

install: build
	@mkdir -p $(BINDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	@echo "installed $(BINDIR)/$(BINARY)"

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

check: fmt vet test

clean:
	rm -f $(BINARY)