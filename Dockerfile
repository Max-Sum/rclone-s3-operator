FROM golang:1.25 AS builder

WORKDIR /workspace

# Enable modules download caching
COPY go.mod ./
# go.sum may not exist yet; go mod download will create it.
RUN go mod download

COPY . .

ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -o /workspace/bin/manager ./cmd/manager

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/bin/manager /manager
USER nonroot:nonroot
ENTRYPOINT ["/manager"]
