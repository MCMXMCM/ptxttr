.PHONY: build-web run test test-js test-e2e fmt tidy vet lint check build-cfn-artifact upload-cfn-artifact deploy deploy-infra deploy-cloudfront grow-prod-volume grow-prod-data-volume validate-cfn build-desktop desktop-build desktop-package desktop-sign

ARTIFACT_BUCKET ?= your-artifact-bucket

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

build-cfn-artifact:
	./scripts/build-cfn-artifact.sh

upload-cfn-artifact:
	./scripts/upload-cfn-artifact.sh --bucket "$(ARTIFACT_BUCKET)"

deploy:
	./scripts/deploy-prod.sh

deploy-infra:
	./scripts/deploy-prod-infra.sh

deploy-cloudfront:
	./scripts/deploy-prod-cloudfront.sh

# Requires AWS credentials with cloudformation:ValidateTemplate (calls the AWS API).
validate-cfn:
	aws cloudformation validate-template \
		--template-body "file://$(CURDIR)/deploy/cloudformation/ptxt-nstr-single-instance.yaml"
	aws cloudformation validate-template \
		--template-body "file://$(CURDIR)/deploy/cloudformation/ptxt-nstr-cloudfront.yaml"

grow-prod-volume:
	./scripts/grow-prod-volume.sh

grow-prod-data-volume:
	./scripts/grow-prod-data-volume.sh

# macOS desktop (Wails). Requires: wails CLI, Xcode CLT; run from a Mac for darwin/universal.
build-desktop: desktop-build

desktop-build:
	./scripts/desktop/build-mac.sh

# Packages whatever .app is already at cmd/desktop/build/bin/ (build first;
# for signed releases run desktop-build → desktop-sign → desktop-package).
desktop-package:
	./scripts/desktop/package-mac.sh

desktop-sign:
	./scripts/desktop/sign-mac.sh
