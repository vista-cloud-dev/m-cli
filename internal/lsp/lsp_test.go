package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, rpcNotification{JSONRPC: "2.0", Method: "demo", Params: map[string]int{"n": 7}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "Content-Length: ") {
		t.Fatalf("missing Content-Length header: %q", buf.String())
	}
	got, err := readMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "demo" {
		t.Errorf("method = %q, want demo", got.Method)
	}
}

func frame(t *testing.T, b *bytes.Buffer, v any) {
	t.Helper()
	if err := writeMessage(b, v); err != nil {
		t.Fatal(err)
	}
}

// readFrames splits a framed output stream into messages.
func readFrames(t *testing.T, data []byte) []*rpcMessage {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(data))
	var out []*rpcMessage
	for {
		m, err := readMessage(r)
		if err != nil {
			break
		}
		out = append(out, m)
	}
	return out
}

func TestServerInitializeAndDiagnostics(t *testing.T) {
	var in bytes.Buffer
	frame(t, &in, rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{}`)})
	frame(t, &in, rpcNotification{JSONRPC: "2.0", Method: "initialized", Params: map[string]any{}})
	open := map[string]any{"textDocument": map[string]any{"uri": "file:///x.m", "text": "EN ;\n do work(.x(1))\n quit\n"}}
	frame(t, &in, rpcNotification{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: open})
	frame(t, &in, rpcNotification{JSONRPC: "2.0", Method: "exit", Params: nil})

	var out bytes.Buffer
	srv, err := New(&in, &out)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if !strings.Contains(out.String(), "documentFormattingProvider") {
		t.Errorf("initialize response missing capabilities:\n%s", out.String())
	}

	var found bool
	for _, m := range readFrames(t, out.Bytes()) {
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p publishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Diagnostics) != 1 {
			t.Fatalf("got %d diagnostics, want 1", len(p.Diagnostics))
		}
		d := p.Diagnostics[0]
		if d.Severity != 1 || d.Code != "M-MOD-037" || d.Source != "m" {
			t.Errorf("diagnostic = %+v, want severity 1 / code M-MOD-037 / source m", d)
		}
		if d.Range.Start.Line != 1 { // 0-based: the `do work(.x(1))` line
			t.Errorf("diagnostic start line = %d, want 1", d.Range.Start.Line)
		}
		found = true
	}
	if !found {
		t.Errorf("no publishDiagnostics with M-MOD-037 in:\n%s", out.String())
	}
}

func TestServerFormatting(t *testing.T) {
	var in bytes.Buffer
	open := map[string]any{"textDocument": map[string]any{"uri": "file:///y.m", "text": "EN ;\n set x=1\n"}}
	frame(t, &in, rpcNotification{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: open})
	fmtReq := map[string]any{"textDocument": map[string]any{"uri": "file:///y.m"}}
	b, _ := json.Marshal(fmtReq)
	frame(t, &in, rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "textDocument/formatting", Params: b})
	frame(t, &in, rpcNotification{JSONRPC: "2.0", Method: "exit", Params: nil})

	var out bytes.Buffer
	srv, err := New(&in, &out)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Serve(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "SET x=1") { // canonical uppercased the keyword
		t.Errorf("formatting did not uppercase the keyword:\n%s", out.String())
	}
}
