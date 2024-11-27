# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
BINARY_NAME=prom-scrape-analyzer-$(GOOS)-$(GOARCH)
VERSION=0.1.0
BUILD_FLAGS=-ldflags "-X main.version=$(VERSION) -s -w"
GOARCH=$(shell go env GOARCH)
GOOS=$(shell go env GOOS)
CGO_ENABLED=0

all: test build

build:
	GOARCH=$(GOARCH) GOOS=$(GOOS) CGO_ENABLED=$(CGO_ENABLED) $(GOBUILD) $(BUILD_FLAGS) -o $(BINARY_NAME) ./cmd/...

test:
	$(GOTEST) ./...

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)

run:
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/main.go
	./$(BINARY_NAME)

deps:
	$(GOGET) ./...
	$(GOMOD) tidy

lint: deps
	golangci-lint run --fix --print-resources-usage ./...

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)_linux -v ./cmd/main.go

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME).exe -v ./cmd/main.go

build-mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)_mac -v ./cmd/main.go

.PHONY: all build test clean run deps build-linux build-windows build-mac
