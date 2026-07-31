.PHONY: build-web run test test-js test-e2e fmt tidy vet lint check desktop-dev desktop-build desktop-package desktop-release

build-web:
	npm run build:web

run: build-web
	go run ./cmd/server

test: build-web
	go test ./...
	$(MAKE) test-js

test-js:
	npm run test:unit

test-e2e: build-web
	@env -i HOME="$$HOME" PATH="$$PATH" USER="$$USER" LOGNAME="$$LOGNAME" \
		GOMODCACHE="$$HOME/go/pkg/mod" GOCACHE="$$HOME/Library/Caches/go-build" \
		go build -o /tmp/ptxt-e2e-server ./cmd/server
	npx playwright install chromium
	PTXT_E2E_SERVER=/tmp/ptxt-e2e-server npm run test:e2e

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

vet: build-web
	go vet ./...

lint: build-web
	golangci-lint run ./...

check: fmt vet lint test

desktop-dev:
	npm run desktop:dev

desktop-build:
	npm run desktop:build

desktop-package:
	npm run desktop:package

desktop-release:
	npm run desktop:release
