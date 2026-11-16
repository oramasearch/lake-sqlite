# SPDX-License-Identifier: MIT

IMG ?= oramasearch/lake-sqlite:dev

.PHONY: build test vet docker-build run

build: ## Build the binary for this machine.
	CGO_ENABLED=0 go build -trimpath -o lake-sqlite ./cmd/lake-sqlite

test: ## Unit and HTTP tests (no network, temp directories only).
	go test ./...

vet:
	go vet ./...

docker-build: ## linux/amd64 image.
	docker buildx build --platform linux/amd64 -t $(IMG) .

run: build ## Serve ./local.db on :8080 with a throwaway token.
	LAKE_SQLITE_TOKEN=local-dev-token LAKE_SQLITE_PATH=./local.db ./lake-sqlite
