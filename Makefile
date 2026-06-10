APP=sbgw
VERSION?=$(shell cat VERSION 2>/dev/null || echo dev)
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE?=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
REGISTRY?=sealos.hub:5000/kube4

.PHONY: run build test tidy docker dockerx run-amd64 run-arm64 run-all clean

run:
	go run ./cmd/sbgw

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o bin/$(APP) ./cmd/sbgw

test:
	go test ./...

tidy:
	go mod tidy

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(APP):$(VERSION) .

dockerx:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(REGISTRY)/$(APP):$(VERSION) .

run-amd64:
	bash build.sh --arch amd64 --version $(VERSION)

run-arm64:
	bash build.sh --arch arm64 --version $(VERSION)

run-all:
	bash build.sh --arch all --version $(VERSION)

clean:
	rm -rf bin dist .build-payload .build-payload-* payload.tar.gz
