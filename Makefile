IMAGE ?= sunshine-host-sampling-controller:dev

.PHONY: fmt vet test build docker check

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -o bin/controller ./cmd/controller

docker:
	docker build -t $(IMAGE) .

check: vet test build
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
