.PHONY: run test fmt tidy vet lint check build-cfn-artifact upload-cfn-artifact deploy deploy-infra

ARTIFACT_BUCKET ?= your-artifact-bucket

run:
	go run ./cmd/server

test:
	go test ./...

fmt:
	gofmt -w cmd internal

tidy:
	go mod tidy

vet:
	go vet ./...

lint:
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
