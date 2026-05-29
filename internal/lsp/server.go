package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-cli/internal/workspace"
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

// Server is a stdio LSP server. It owns one parser, a document store, and a
// cache of linters keyed by the project config governing each document — so a
// file's diagnostics use the SAME resolved rule set `m lint`/pre-commit would
// (G2). The message loop is single-threaded, so the parser is never used
// concurrently. Build with New, run with Serve, release with Close.
type Server struct {
	in      *bufio.Reader
	out     io.Writer
	p       *parse.Parser
	root    string                      // workspace root path (from initialize rootUri); "" ⇒ none
	linters map[string]*lint.Linter     // keyed by resolved config-file path ("" ⇒ no config ⇒ default profile)
	indexes map[string]*workspace.Index // workspace index cached per scope dir (lazy; for cross-routine rules)
	docs    map[string]string
}

// New builds a server reading framed messages from in and writing to out.
func New(in io.Reader, out io.Writer) (*Server, error) {
	p, err := parse.New(context.Background())
	if err != nil {
		return nil, err
	}
	return &Server{in: bufio.NewReader(in), out: out, p: p,
		linters: map[string]*lint.Linter{}, indexes: map[string]*workspace.Index{}, docs: map[string]string{}}, nil
}

// Close releases every cached linter and the parser.
func (s *Server) Close() {
	for _, l := range s.linters {
		l.Close()
	}
	_ = s.p.Close(context.Background())
}

// linterFor returns the linter for the project config governing dir, building
// and caching it on first use. It resolves the SAME rule set the CLI does
// (lint.ResolveFilter + OptionsFromConfig + Resolve), so editor diagnostics
// cannot drift from `m lint --check` / pre-commit (G2). A malformed config
// falls back to defaults rather than killing the editor session.
func (s *Server) linterFor(dir string) (*lint.Linter, error) {
	key := config.FindConfig(dir) // "" when no project config governs dir
	if l, ok := s.linters[key]; ok {
		return l, nil
	}
	cfg, err := config.LoadConfig(dir)
	if err != nil {
		cfg = config.Config{}
	}
	rules, err := lint.Resolve(lint.ResolveFilter("", cfg), lint.OptionsFromConfig(cfg))
	if err != nil {
		return nil, err
	}
	l, err := lint.NewLinter(s.p, rules)
	if err != nil {
		return nil, err
	}
	// Cross-routine rules (M-XINDX-007/008/049) need a workspace index — the same
	// pre-pass `m lint` builds over its fileset. Attach one scoped to the workspace
	// root (or the file's dir when no root was advertised), matching the CLI.
	needsWS := false
	for _, r := range rules {
		if r.NeedsWorkspace() {
			needsWS = true
			break
		}
	}
	if needsWS {
		scope := s.root
		if scope == "" {
			scope = dir
		}
		l.AttachWorkspace(s.workspaceIndexFor(scope))
	}
	s.linters[key] = l
	return l, nil
}

// workspaceIndexFor lazily builds and caches a workspace index over every .m
// routine under scope — the disk snapshot the cross-routine rules resolve
// against, mirroring `m lint`'s pre-pass. Built from disk (like the CLI/CI run),
// so an unsaved buffer's edits are not reflected until saved — the same
// editor-vs-disk skew every language server has, and orthogonal to rule/config
// parity (G2).
func (s *Server) workspaceIndexFor(scope string) *workspace.Index {
	if idx, ok := s.indexes[scope]; ok {
		return idx
	}
	idx := workspace.New()
	_ = filepath.Walk(scope, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".m" {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		tree, perr := s.p.Parse(context.Background(), src)
		if perr != nil {
			return nil
		}
		idx.AddFile(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), tree.RootNode())
		tree.Close()
		return nil
	})
	s.indexes[scope] = idx
	return idx
}

// initializeParams is the subset of the LSP `initialize` request we read: the
// workspace root, by which we scope the cross-routine workspace index.
type initializeParams struct {
	RootURI          string `json:"rootUri"`
	RootPath         string `json:"rootPath"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
}

// root resolves the workspace root to a filesystem path, preferring rootUri,
// then the first workspace folder, then the legacy rootPath.
func (p initializeParams) root() string {
	if p.RootURI != "" {
		return uriToPath(p.RootURI)
	}
	if len(p.WorkspaceFolders) > 0 && p.WorkspaceFolders[0].URI != "" {
		return uriToPath(p.WorkspaceFolders[0].URI)
	}
	return p.RootPath
}

// uriToPath converts a file:// document URI to a filesystem path (percent-
// decoded). It returns "" for non-file URIs, which makes diagnostics fall back
// to the default profile from CWD.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
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
		var ip initializeParams
		if json.Unmarshal(m.Params, &ip) == nil {
			if root := ip.root(); root != "" {
				s.root = root
			}
		}
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

// publish lints the document with the project-config-resolved linter and the
// routine name from the URI, then pushes diagnostics — the same findings
// `m lint` would produce for that file (G2).
func (s *Server) publish(uri string) error {
	dir, routine := ".", ""
	if path := uriToPath(uri); path != "" {
		dir = filepath.Dir(path)
		routine = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	diags := []diagnostic{}
	if lntr, err := s.linterFor(dir); err == nil {
		if findings, lerr := lntr.LintNamed(context.Background(), []byte(s.docs[uri]), routine); lerr == nil {
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
