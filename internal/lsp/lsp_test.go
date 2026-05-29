package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/workspace"
	"github.com/vista-cloud-dev/m-parse/parse"
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

// TestServerDiagnosticsHonorProjectConfig is the G2 parity test: the LSP must
// publish exactly the diagnostics `m lint` would for the same source, project
// config, and routine name — no editor↔CI drift. It writes a .m-cli.toml that
// selects the `xindex` profile and opens a routine whose first label differs
// from its filename, which fires M-XINDX-017 (a routine-identity rule that needs
// both the config-selected profile AND the routine name from the URI). The old
// hardcoded-default + Lint("") server cannot produce it.
func TestServerDiagnosticsHonorProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".m-cli.toml"), []byte("[lint]\nrules = \"xindex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "ZZTAG ;;1.0;TEST;;May 29, 2026\n W 1\n Q\n"
	path := filepath.Join(dir, "FOO.m")
	uri := "file://" + path

	// Parity oracle: what `m lint` resolves for this file + config.
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	cfg, err := config.LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := lint.Resolve(lint.ResolveFilter("", cfg), lint.OptionsFromConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := lint.NewLinter(p, rules)
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	// The xindex profile carries cross-routine rules (007/008/049) that need a
	// workspace index — exactly the pre-pass `m lint` builds over the linted
	// files. Build the same index here so the oracle reflects the CLI's full
	// finding set (incl. M-XINDX-049 orphan-label), not just document-scoped rules.
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := workspace.New()
	tree, perr := p.Parse(context.Background(), []byte(src))
	if perr != nil {
		t.Fatal(perr)
	}
	ws.AddFile("FOO", tree.RootNode())
	tree.Close()
	oracle.AttachWorkspace(ws)
	wantFindings, err := oracle.LintNamed(context.Background(), []byte(src), "FOO")
	if err != nil {
		t.Fatal(err)
	}
	wantCodes := codeSet(func() []string {
		var cs []string
		for _, f := range wantFindings {
			cs = append(cs, f.Rule)
		}
		return cs
	}())
	if !contains(wantCodes, "M-XINDX-017") || !contains(wantCodes, "M-XINDX-049") {
		t.Fatalf("oracle precondition failed: xindex profile should fire M-XINDX-017 (identity) + M-XINDX-049 (orphan label via workspace index); got %v", wantCodes)
	}

	// Drive the server over the same document, advertising the workspace root so
	// the LSP builds the same workspace index the CLI does.
	rootURI := "file://" + dir
	initParams, _ := json.Marshal(map[string]any{"rootUri": rootURI})
	var in bytes.Buffer
	frame(t, &in, rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: initParams})
	open := map[string]any{"textDocument": map[string]any{"uri": uri, "text": src}}
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

	var gotCodes []string
	for _, m := range readFrames(t, out.Bytes()) {
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var pd publishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &pd); err != nil {
			t.Fatal(err)
		}
		if pd.URI != uri {
			continue
		}
		for _, d := range pd.Diagnostics {
			gotCodes = append(gotCodes, d.Code)
		}
	}
	if got, want := codeSet(gotCodes), wantCodes; !equalStr(got, want) {
		t.Errorf("LSP diagnostics drifted from `m lint`\n got:  %v\n want: %v", got, want)
	}
}

func codeSet(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
