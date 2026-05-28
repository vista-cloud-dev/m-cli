package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// --- minimal LSP wire types (0-based line/character) -------------------------

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Code     string   `json:"code,omitempty"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type docRefParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// Server is a stdio LSP server. It owns one parser + a linter and a document
// store; the message loop is single-threaded, so the parser is never used
// concurrently. Build with New, run with Serve, release with Close.
type Server struct {
	in   *bufio.Reader
	out  io.Writer
	p    *parse.Parser
	lntr *lint.Linter
	docs map[string]string
}

// New builds a server reading framed messages from in and writing to out.
func New(in io.Reader, out io.Writer) (*Server, error) {
	p, err := parse.New(context.Background())
	if err != nil {
		return nil, err
	}
	lntr, err := lint.NewLinter(p, lint.Profile("default"))
	if err != nil {
		_ = p.Close(context.Background())
		return nil, err
	}
	return &Server{in: bufio.NewReader(in), out: out, p: p, lntr: lntr, docs: map[string]string{}}, nil
}

// Close releases the linter and parser.
func (s *Server) Close() {
	s.lntr.Close()
	_ = s.p.Close(context.Background())
}

// Serve runs the message loop until `exit` (or EOF).
func (s *Server) Serve() error {
	for {
		m, err := readMessage(s.in)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		stop, err := s.handle(m)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}

func (s *Server) handle(m *rpcMessage) (stop bool, err error) {
	switch m.Method {
	case "initialize":
		return false, s.reply(m.ID, s.capabilities())
	case "initialized":
		return false, nil
	case "shutdown":
		return false, s.reply(m.ID, nil)
	case "exit":
		return true, nil

	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(m.Params, &p) != nil {
			return false, nil
		}
		s.docs[p.TextDocument.URI] = p.TextDocument.Text
		return false, s.publish(p.TextDocument.URI)

	case "textDocument/didChange":
		var p didChangeParams
		if json.Unmarshal(m.Params, &p) != nil {
			return false, nil
		}
		if n := len(p.ContentChanges); n > 0 { // full sync: last change is the doc
			s.docs[p.TextDocument.URI] = p.ContentChanges[n-1].Text
		}
		return false, s.publish(p.TextDocument.URI)

	case "textDocument/didClose":
		var p docRefParams
		if json.Unmarshal(m.Params, &p) != nil {
			return false, nil
		}
		delete(s.docs, p.TextDocument.URI)
		return false, s.notify("textDocument/publishDiagnostics",
			publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []diagnostic{}})

	case "textDocument/formatting":
		var p docRefParams
		if json.Unmarshal(m.Params, &p) != nil {
			return false, s.reply(m.ID, []textEdit{})
		}
		return false, s.reply(m.ID, s.format(p.TextDocument.URI))

	default:
		if len(m.ID) > 0 { // unknown request → method-not-found; ignore unknown notifications
			return false, s.replyError(m.ID, -32601, "method not found: "+m.Method)
		}
		return false, nil
	}
}

func (s *Server) capabilities() map[string]any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":           1, // full document sync
			"documentFormattingProvider": true,
		},
		"serverInfo": map[string]any{"name": "m"},
	}
}

// publish lints the document and pushes diagnostics for it.
func (s *Server) publish(uri string) error {
	findings, err := s.lntr.Lint(context.Background(), []byte(s.docs[uri]))
	diags := []diagnostic{}
	if err == nil {
		for _, f := range findings {
			diags = append(diags, diagnostic{
				Range: lspRange{
					Start: position{Line: f.Line - 1, Character: f.Col - 1},
					End:   position{Line: f.EndLine - 1, Character: f.EndCol - 1},
				},
				Severity: lspSeverity(f.Severity),
				Code:     f.Rule,
				Source:   "m",
				Message:  f.Message,
			})
		}
	}
	return s.notify("textDocument/publishDiagnostics",
		publishDiagnosticsParams{URI: uri, Diagnostics: diags})
}

// format runs `m fmt --rules=canonical` and returns a whole-document edit (or
// none if already formatted).
func (s *Server) format(uri string) []textEdit {
	src := []byte(s.docs[uri])
	out, err := mfmt.Format(context.Background(), s.p, src, mfmt.Rules(mfmt.Canonical))
	if err != nil || string(out) == string(src) {
		return []textEdit{}
	}
	return []textEdit{{
		Range:   lspRange{Start: position{0, 0}, End: endPosition(src)},
		NewText: string(out),
	}}
}

func lspSeverity(s lint.Severity) int {
	switch s {
	case lint.Error:
		return 1
	case lint.Warning:
		return 2
	case lint.Info:
		return 3
	default: // style → hint
		return 4
	}
}

// endPosition is the LSP position just past the last byte of src.
func endPosition(src []byte) position {
	line, col := 0, 0
	for _, b := range src {
		if b == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return position{Line: line, Character: col}
}

func (s *Server) reply(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return writeMessage(s.out, rpcResponse{JSONRPC: "2.0", ID: id, Result: raw})
}

func (s *Server) replyError(id json.RawMessage, code int, msg string) error {
	return writeMessage(s.out, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) notify(method string, params any) error {
	return writeMessage(s.out, rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}
