FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

ARG VERSION=dev

COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/reddit-mcp ./cmd/reddit-mcp

FROM gcr.io/distroless/static-debian12:nonroot

# The MCP Registry verifies image ownership through this annotation; it must
# match the "name" field in server.json.
LABEL io.modelcontextprotocol.server.name="io.github.rishenco/reddit-mcp"
LABEL org.opencontainers.image.source="https://github.com/rishenco/reddit-mcp"

COPY --from=builder /out/reddit-mcp /reddit-mcp
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/reddit-mcp"]
