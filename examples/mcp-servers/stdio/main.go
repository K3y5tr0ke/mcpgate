package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/K3y5tr0ke/mcpgate/examples/mcp-servers/common"
)

func main() {
	serverName := os.Getenv("MCP_SERVER_NAME")
	if serverName == "" {
		serverName = "stdio"
	}

	h := common.NewHandler(serverName)

	// Startup message goes to stderr only — stdout is reserved for JSON-RPC.
	fmt.Fprintf(os.Stderr, "mcp-stdio-example: ready server=%s\n", serverName)

	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer to 1 MiB to handle large tool arguments.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req common.Request
		if err := json.Unmarshal(line, &req); err != nil {
			// Write a parse-error response with a null id and move on.
			resp := append(common.ParseErrorResponse(), '\n')
			_, _ = os.Stdout.Write(resp)
			continue
		}

		resp := append(h.Handle(&req), '\n')
		_, _ = os.Stdout.Write(resp)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-stdio-example: read error:", err)
		os.Exit(1)
	}
}
