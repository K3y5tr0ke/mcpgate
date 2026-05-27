FROM golang:1.26-alpine AS builder
WORKDIR /src
# Download dependencies in a separate layer to leverage Docker cache.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" \
    -o /mcp-streamable-example \
    ./examples/mcp-servers/streamable-http

FROM alpine:3.21
RUN adduser -D -u 1000 mcp
USER mcp
COPY --from=builder /mcp-streamable-example /usr/local/bin/mcp-streamable-example
ENV MCP_SERVER_NAME=streamable-http
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/mcp-streamable-example"]
