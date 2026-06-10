FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git

# Copy module files first for better Docker layer cache.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# BuildKit injects TARGETOS/TARGETARCH from --platform automatically.
# Do not set defaults here, otherwise arm64 builds can be accidentally compiled as amd64.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

# The repository may be bootstrapped without go.sum. Tidy inside the builder
# makes CI/docker builds deterministic enough to pass even before go.sum is committed.
# You should still run `go mod tidy` locally and commit go.sum after dependencies settle.
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /out/sbgw ./cmd/sbgw

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/sbgw /usr/local/bin/sbgw
COPY config.example.yaml /app/config.yaml
EXPOSE 12224
ENTRYPOINT ["/usr/local/bin/sbgw"]
