FROM golang:1.26-alpine AS builder
WORKDIR /src
# Download dependencies in a separate layer to leverage Docker cache.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" \
    -o /mcp-sse-example \
    ./examples/mcp-servers/sse

FROM alpine:3.21
RUN adduser -D -u 1000 mcp
USER mcp
COPY --from=builder /mcp-sse-example /usr/local/bin/mcp-sse-example
ENV MCP_SERVER_NAME=sse
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/mcp-sse-example"]
