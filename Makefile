BINARY := nuon-ext-override-lint
INSTALL_DIR ?= $(HOME)/.config/nuon/extensions/$(BINARY)

.DEFAULT_GOAL := build
.PHONY: build test fmt vet lint tidy clean install all

build:
	go build -o $(BINARY) .

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
	rm -rf dist/

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) nuon-ext.toml manifest.json $(INSTALL_DIR)/

all: fmt vet test build
