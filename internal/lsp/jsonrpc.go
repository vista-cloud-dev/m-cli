// Package lsp is m-cli's Language Server (spec §3.1): LSP 3.x over stdio,
// reusing the lint and fmt engines so the editor shows exactly the diagnostics
// CI produces. JSON-RPC framing is hand-rolled on the stdlib to keep the SBOM
// minimal (no LSP-library dependency). This first increment wires the
// foundation — lifecycle, full text sync, push diagnostics, and formatting;
// hover/completion/definition/symbols/code-actions layer on later.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// rpcMessage is an incoming JSON-RPC 2.0 request or notification (no id ⇒
// notification).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// readMessage reads one Content-Length-framed JSON-RPC message.
func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line ends the header block
		}
		if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q: %w", line, err)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: message without Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m rpcMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("lsp: bad message body: %w", err)
	}
	return &m, nil
}

// writeMessage writes v as a Content-Length-framed JSON message.
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}
