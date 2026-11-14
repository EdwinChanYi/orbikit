SERVICES := core chatroom gateway webhook admin
GOBIN    := $(shell go env GOPATH)/bin

.PHONY: all build test lint proto tools up down $(SERVICES)

all: lint test build

build: $(SERVICES)

$(SERVICES):
	go build -o bin/$@ ./cmd/$@

test:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run

tools:
	go install github.com/bufbuild/buf/cmd/buf@v1.34.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0

proto:
	PATH="$(GOBIN):$$PATH" buf generate

up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down
