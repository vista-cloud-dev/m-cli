package lint

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// XINDEX rule family — faithful port of the Python tool's lint/rules.py
// (M-XINDX-NN, where NN mirrors the VA VistA Toolkit ^XINDEX numeric code 1:1).
// These carry the "xindex" tag; the 31 that map to a documented VA SAC section
// also carry "sac"; the 8 VistA-Kernel-specific ones also carry "vista". The
// node types below were probed against the real tree-sitter-m grammar (the
// Python source notes several node-name typos that silently no-op'd — those are
// corrected here).
//
// The cross-routine rules M-XINDX-007 (call to undefined routine), 008
// (undefined label in another routine) and 049 (label never referenced) live in
// xindex_cross.go: they consume the workspace index (internal/workspace) and run
// only when one is attached, mirroring the Python tool's needs_context gating.

// xindexAll returns the ported XINDEX rules. Rule 017/014 are name-aware;
// 007/008/049 are cross-routine (need a workspace index — skipped without one);
// the rest are plain walk rules. trusted is the M-XINDX-007 allowlist.
func xindexAll(trusted map[string]bool) []Rule {
	return []Rule{
		ruleUndefinedRoutine(trusted), // M-XINDX-007 (cross-routine)
		ruleUndefinedLabel,            // M-XINDX-008 (cross-routine)
		ruleLabelNeverReferenced,      // M-XINDX-049 (cross-routine)
		ruleZCommand,                  // M-XINDX-002
		ruleDeadCodeAfterQuit,         // M-XINDX-009
		ruleTrailingBlanks,            // M-XINDX-013
		ruleMissingLabelCall,          // M-XINDX-014
		ruleDuplicateLabel,            // M-XINDX-015
		ruleFirstLabelName,            // M-XINDX-017
		ruleControlChar,               // M-XINDX-018
		ruleLineLength245,             // M-XINDX-019
		ruleViewCommand,               // M-XINDX-020
		ruleSyntaxError,               // M-XINDX-021
		ruleExclusiveKill,             // M-XINDX-022
		ruleUnargumentedKill,          // M-XINDX-023
		ruleKillUnsubGlobal,           // M-XINDX-024
		ruleBreakCommand,              // M-XINDX-025
		ruleNewExclUnarg,              // M-XINDX-026
		ruleDollarView,                // M-XINDX-027
		ruleNonStandardZSV,            // M-XINDX-028
		ruleCloseCommand,              // M-XINDX-029
		ruleLabelOffset,               // M-XINDX-030
		ruleNonStandardZFunc,          // M-XINDX-031
		ruleHaltCommand,               // M-XINDX-032
		ruleReadNoTimeout,             // M-XINDX-033
		ruleOpenCommand,               // M-XINDX-034
		ruleRoutineSize,               // M-XINDX-035
		ruleJobCommand,                // M-XINDX-036
		ruleStarPoundRead,             // M-XINDX-041
		ruleNullLine,                  // M-XINDX-042
		ruleSecondLineSAC,             // M-XINDX-044
		ruleSetPercentGlobal,          // M-XINDX-045
		ruleLowercaseCommand,          // M-XINDX-047
		ruleExtendedReference,         // M-XINDX-050
		ruleEmptyConditional,          // M-XINDX-051
		ruleSystemAccess,              // M-XINDX-054
		rulePatchMissing,              // M-XINDX-056
		ruleLocalVarCase,              // M-XINDX-057
		ruleRoutineCodeSize,           // M-XINDX-058
		ruleLockNoTimeout,             // M-XINDX-060
		ruleNonIncrementalLock,        // M-XINDX-061
		ruleFirstLineSAC,              // M-XINDX-062
	}
}

// --- shared helpers ----------------------------------------------------------

func childType(n parse.Node, typ string) (parse.Node, bool) {
	for i := uint32(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Type() == typ {
			return c, true
		}
	}
	return parse.Node{}, false
}

func cmdKeyword(cmd parse.Node) (parse.Node, string, bool) {
	kw, ok := childType(cmd, "command_keyword")
	if !ok {
		return parse.Node{}, "", false
	}
	return kw, strings.ToUpper(string(kw.Text())), true
}

// forCommands visits every command node and calls fn with the command, its
// keyword node, and the upper-cased keyword text.
func forCommands(root parse.Node, fn func(cmd, kwNode parse.Node, kw string)) {
	walkNodes(root, func(n parse.Node) {
		if n.Type() != "command" {
			return
		}
		if kwNode, kw, ok := cmdKeyword(n); ok {
			fn(n, kwNode, kw)
		}
	})
}

// cmdArguments returns the argument children of a command's argument_list (nil
// if the command has no argument_list).
func cmdArguments(cmd parse.Node) []parse.Node {
	al, ok := childType(cmd, "argument_list")
	if !ok {
		return nil
	}
	var out []parse.Node
	for i := uint32(0); i < al.ChildCount(); i++ {
		if c := al.Child(i); c.Type() == "argument" {
			out = append(out, c)
		}
	}
	return out
}

func hasArgList(cmd parse.Node) bool {
	_, ok := childType(cmd, "argument_list")
	return ok
}

// argPayload returns an argument's first non-punctuation child.
func argPayload(arg parse.Node) (parse.Node, bool) {
	for i := uint32(0); i < arg.ChildCount(); i++ {
		c := arg.Child(i)
		switch c.Type() {
		case "(", ")", ",":
			continue
		}
		return c, true
	}
	return parse.Node{}, false
}

func argHasTimeout(arg parse.Node) bool {
	_, ok := childType(arg, "argument_postconditional")
	return ok
}

func cmdHasPostcond(cmd parse.Node) bool {
	if _, ok := childType(cmd, "postconditional"); ok {
		return true
	}
	_, ok := childType(cmd, "argument_postconditional")
	return ok
}

// findNode builds a finding spanning a node (1-based positions).
func findNode(n parse.Node, msg string) Finding {
	s, e := n.StartPoint(), n.EndPoint()
	return Finding{
		Message: msg,
		Line:    int(s.Row) + 1, Col: int(s.Column) + 1,
		EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
	}
}

// splitLines mimics Python bytes.splitlines: split on \n, strip a trailing \r
// per line, and drop the final empty element when the source ends in \n.
func splitLines(src []byte) [][]byte {
	if len(src) == 0 {
		return nil
	}
	parts := bytes.Split(src, []byte("\n"))
	if n := len(parts); n > 0 && len(parts[n-1]) == 0 {
		parts = parts[:n-1]
	}
	for i := range parts {
		parts[i] = bytes.TrimSuffix(parts[i], []byte("\r"))
	}
	return parts
}

// topLevelLines returns the source_file's "line" children in document order.
func topLevelLines(root parse.Node) []parse.Node {
	var out []parse.Node
	for i := uint32(0); i < root.ChildCount(); i++ {
		if c := root.Child(i); c.Type() == "line" {
			out = append(out, c)
		}
	}
	return out
}

var (
	reLeadingZ  = regexp.MustCompile(`^\$Z[A-Z]*`)
	rePatchList = regexp.MustCompile(`\*\*[^*]*\*\*`)
	// re044SAC is the faithful translation of XINDEX's 2nd-line SAC M-pattern
	// `1.2N1"."1.2N.1(1"T",1"V").2N1";"1A.APN1";".E` applied to $P(line,";",3,99):
	// version `N.N[T|V]N?` ; package `<alpha><printable>*` ; rest. Verified to
	// reproduce XINDEX 1:1 (1612/1612, zero FP) over its scanned corpus.
	re044SAC = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}[TV]?[0-9]{0,2};[A-Za-z][ -~]*;`)
)

// mIsUP reports whether a byte matches the MUMPS pattern class `U` or `P`
// (uppercase or punctuation) — printable ASCII that is neither lowercase nor a
// digit. Space (32) counts as punctuation, matching the engine XINDEX runs on.
func mIsUP(b byte) bool {
	return b >= 32 && b <= 126 && !(b >= 'a' && b <= 'z') && !(b >= '0' && b <= '9')
}

// semiPiece returns the n-th (1-based) `;`-delimited piece of b ($P(b,";",n)).
func semiPiece(b []byte, n int) []byte {
	parts := bytes.Split(b, []byte(";"))
	if n >= 1 && n <= len(parts) {
		return parts[n-1]
	}
	return nil
}

// semiPieceFrom returns pieces n..end joined by `;` ($P(b,";",n,99)).
func semiPieceFrom(b []byte, n int) []byte {
	parts := bytes.Split(b, []byte(";"))
	if n >= 1 && n <= len(parts) {
		return bytes.Join(parts[n-1:], []byte(";"))
	}
	return nil
}

// firstLineSACok is XINDEX rule 62's check: the first line's 2nd `;`-piece must
// match `1.UP1"/"1.UP1"-".E` — uppercase/punctuation `SITE/DEV-` author prefix
// (no lowercase or digits before the `-`), then anything. Verified 1:1 vs
// XINDEX (2990/2990, zero FP).
func firstLineSACok(line []byte) bool {
	p := semiPiece(line, 2)
	for k := 0; k < len(p); k++ {
		if p[k] != '-' {
			continue
		}
		pre := p[:k]
		allUP := true
		for i := 0; i < len(pre); i++ {
			if !mIsUP(pre[i]) {
				allUP = false
				break
			}
		}
		if !allUP {
			continue
		}
		// `1.UP1"/"1.UP` needs ≥1 char before the `/` and ≥1 between `/` and `-`.
		for i := 1; i <= len(pre)-2; i++ {
			if pre[i] == '/' {
				return true
			}
		}
	}
	return false
}

func hasLowercaseLetter(s string) bool {
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			return true
		}
	}
	return false
}

// --- text / line rules -------------------------------------------------------

// M-XINDX-013 — Blank(s) at end of line. Faithful to XINDEX's two command-loop
// triggers (XINDEX.m), verified against live ^XINDEX over the corpus bytes:
//
//   - SEP path (`D SEP I '$L(LIN),CH=" " D E^XINDX1(13)`): a trailing space
//     after a command ARGUMENT — ` S X=1 `, ` W "x" `, ` D EN^FOO ` fire.
//   - command-position path (`I COM=" " S ERR=$S(LIN?1." ":13,1:0)`): an
//     ARGUMENTLESS command leaves one space as its terminator, so a single
//     trailing space (` Q `, ` D `, ` H `, ` Q:1 `) is NOT flagged, but two or
//     more (` Q  `, ` DO  `) leave a leftover space in command position → 013.
//
// So per command on the line: if it has an argument, ≥1 trailing space to EOL
// fires; if it is argumentless, ≥2 trailing spaces fire. A comment consumes the
// rest of its line (no command ends at the blanks) and a whitespace-only line is
// neither 013 nor 042 (XINDEX consumes it as dot/space and quits) — both clean.
var ruleTrailingBlanks = Rule{
	ID: "M-XINDX-013", Severity: Style, Category: "style",
	Title: "Blank(s) at end of line", Tags: []string{"xindex"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		var out []Finding
		var walk func(n parse.Node)
		walk = func(n parse.Node) {
			if n.Type() == "command" {
				hasArg := false
				for i := uint32(0); i < n.ChildCount(); i++ {
					if n.Child(i).Type() == "argument_list" {
						hasArg = true
						break
					}
				}
				e := int(n.EndByte())
				run := trailingSpaceRun(src, e) // spaces from e to EOL; 0 if not trailing
				if (hasArg && run >= 1) || (!hasArg && run >= 2) {
					line, col := lineColAt(src, e)
					out = append(out, Finding{
						Message: "Blank(s) at end of line",
						Line:    line, Col: col, EndLine: line, EndCol: col + run,
					})
				}
			}
			for i := uint32(0); i < n.ChildCount(); i++ {
				walk(n.Child(i))
			}
		}
		walk(root)
		return out
	},
}

// trailingSpaceRun returns the count of consecutive spaces from off up to the
// line terminator (or end of src). It returns 0 if a non-space, non-terminator
// byte appears first — i.e. the spaces are not at end-of-line. Faithful to
// XINDEX, whose 013 triggers test the space character specifically (a trailing
// tab is a control character → M-XINDX-018, not 013).
func trailingSpaceRun(src []byte, off int) int {
	i := off
	for i < len(src) {
		switch src[i] {
		case ' ':
			i++
		case '\n', '\r':
			return i - off
		default:
			return 0
		}
	}
	return i - off // trailing spaces at end-of-file with no final newline
}

// lineColAt returns the 1-based line and column of byte offset off in src.
func lineColAt(src []byte, off int) (line, col int) {
	line = 1
	lineStart := 0
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return line, off - lineStart + 1
}

// M-XINDX-018 — Line contains a CONTROL (non-graphic) character.
var ruleControlChar = Rule{
	ID: "M-XINDX-018", Severity: Style, Category: "style",
	Title: "Line contains a CONTROL (non-graphic) character", Tags: []string{"xindex"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		var out []Finding
		for i, raw := range splitLines(src) {
			for col, b := range raw {
				if (b < 32 && b != '\t') || b == 127 {
					out = append(out, Finding{
						Message: fmt.Sprintf("Line contains a CONTROL (non-graphic) character (byte 0x%02x)", b),
						Line:    i + 1, Col: col + 1, EndLine: i + 1, EndCol: col + 2,
					})
					break // one diagnostic per line is enough
				}
			}
		}
		return out
	},
}

// M-XINDX-019 — Line is longer than 245 bytes (the legacy SACC limit; M-MOD-001
// supersedes it with a configurable threshold, but the xindex profile keeps the
// faithful 245-byte rule).
var ruleLineLength245 = Rule{
	ID: "M-XINDX-019", Severity: Style, Category: "style",
	Title: "Line is longer than 245 bytes", Tags: []string{"xindex", "sac"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		var out []Finding
		for i, raw := range splitLines(src) {
			if len(raw) > 245 {
				out = append(out, Finding{
					Message: fmt.Sprintf("Line is longer than 245 bytes (%d bytes)", len(raw)),
					Line:    i + 1, Col: 246, EndLine: i + 1, EndCol: len(raw) + 1,
				})
			}
		}
		return out
	},
}

// M-XINDX-042 — Null line (no commands or comment). XINDEX flags a line as null
// when, after stripping the leading space-piece, nothing remains
// (`S LIN=$P(LIN," ",2,999) I LIN="" D E^XINDX1(42)`): verified against live
// ^XINDEX, this is true for an empty line and a single-space line, but a line of
// TWO-OR-MORE spaces is consumed as dot/space level and is clean (not 042).
// (XINDEX additionally raises M-XINDX-018 on a truly empty line — an XINDEX
// parser quirk treating "" as a non-graphic line; m-cli keeps 018 about real
// control characters and does not replicate that.)
var ruleNullLine = Rule{
	ID: "M-XINDX-042", Severity: Style, Category: "style",
	Title: "Null line (no commands or comment)", Tags: []string{"xindex"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		var out []Finding
		// XINDEX raises 042 iff `$P(line," ",2,999)=""` — nothing remains after the
		// first space-piece. Verified against live ^XINDEX, that is exactly: the line
		// has NO space, or exactly ONE space as its final character. This covers an
		// empty line, a single-space line, and a body-less label (`TAG`, `TAG `,
		// `TAG(A,B)`, `TAG(A,B) `). A label with TWO+ trailing spaces, a comment, or
		// code leaves a residue after the first space and is clean. Pure text — it
		// mirrors XINDEX's own line model, so it also matches on malformed input
		// where the parse tree would mis-split.
		for i, raw := range splitLines(src) {
			if sp := bytes.IndexByte(raw, ' '); sp < 0 || sp == len(raw)-1 {
				out = append(out, Finding{
					Message: "Null line (no commands or comment)",
					Line:    i + 1, Col: 1, EndLine: i + 1, EndCol: 1,
				})
			}
		}
		return out
	},
}

// routineSizes computes XINDEX's two size totals exactly (XINDEX.m B5 loop):
// SZT = Σ(len(line)+2) over every line (the +2 is XINDEX's CRLF accounting), and
// SZC = Σ(len) of comment lines — a line whose content after the first
// space-piece (and any leading dot/space) begins with a single ";" (";;" version
// lines are excluded). Routine code size is SZT-SZC.
func routineSizes(src []byte) (szt, szc int) {
	for _, line := range splitLines(src) {
		szt += len(line) + 2
		sp := bytes.IndexByte(line, ' ')
		if sp < 0 {
			continue // no space ⇒ no comment part (a bare label, etc.)
		}
		afterLabel := bytes.TrimLeft(line[sp+1:], " .")
		if len(afterLabel) >= 1 && afterLabel[0] == ';' && (len(afterLabel) < 2 || afterLabel[1] != ';') {
			szc += len(afterLabel)
		}
	}
	return szt, szc
}

// M-XINDX-035 — Routine exceeds SACC maximum size of 20000 bytes (XINDEX SZT).
var ruleRoutineSize = Rule{
	ID: "M-XINDX-035", Severity: Style, Category: "complexity",
	Title: "Routine exceeds SACC maximum size of 20000 bytes", Tags: []string{"xindex", "sac"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		if szt, _ := routineSizes(src); szt > 20000 {
			return []Finding{{
				Message: fmt.Sprintf("Routine exceeds SACC maximum size of 20000 bytes (%d bytes)", szt),
				Line:    1, Col: 1, EndLine: 1, EndCol: 1,
			}}
		}
		return nil
	},
}

// M-XINDX-058 — Routine code (SZT-SZC) exceeds SACC max of 15000 bytes.
var ruleRoutineCodeSize = Rule{
	ID: "M-XINDX-058", Severity: Style, Category: "complexity",
	Title: "Routine code exceeds SACC max of 15000 bytes", Tags: []string{"xindex", "sac"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		if szt, szc := routineSizes(src); szt-szc > 15000 {
			return []Finding{{
				Message: fmt.Sprintf("Routine code exceeds SACC maximum of 15000 bytes (%d bytes)", szt-szc),
				Line:    1, Col: 1, EndLine: 1, EndCol: 1,
			}}
		}
		return nil
	},
}

// M-XINDX-044 — 2nd line of routine violates the SAC. Faithful to XINDEX: the
// check runs only for routines with >2 lines, and the 2nd line's pieces 3..end
// must match the `;;version;package;…` M-pattern (re044SAC).
var ruleSecondLineSAC = Rule{
	ID: "M-XINDX-044", Severity: Info, Category: "documentation",
	Title: "2nd line of routine violates the SAC", Tags: []string{"xindex", "sac", "vista"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		lines := splitLines(src)
		if len(lines) <= 2 {
			return nil // XINDEX guard: LC>2
		}
		if !re044SAC.Match(semiPieceFrom(lines[1], 3)) {
			return []Finding{{
				Message: "2nd line of routine violates the SAC (must be ';;version;package;...;date;build')",
				Line:    2, Col: 1, EndLine: 2, EndCol: 1,
			}}
		}
		return nil
	},
}

// M-XINDX-056 — Patch number missing from second line.
var rulePatchMissing = Rule{
	ID: "M-XINDX-056", Severity: Info, Category: "documentation",
	Title: "Patch number missing from second line", Tags: []string{"xindex", "sac", "vista"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		lines := splitLines(src)
		if len(lines) < 2 {
			return nil
		}
		second := lines[1]
		if !rePatchList.Match(second) && bytes.HasPrefix(bytes.TrimLeft(second, " \t"), []byte(";;")) {
			return []Finding{{
				Message: "Patch number missing from second line (expected `**patch_list**`)",
				Line:    2, Col: 1, EndLine: 2, EndCol: 1,
			}}
		}
		return nil
	},
}

// M-XINDX-062 — First line of routine violates the SAC. Faithful to XINDEX:
// runs only for routines with >2 lines; the first line's 2nd `;`-piece must be
// a `SITE/DEV-description` author prefix (firstLineSACok).
var ruleFirstLineSAC = Rule{
	ID: "M-XINDX-062", Severity: Info, Category: "documentation",
	Title: "First line of routine violates the SAC", Tags: []string{"xindex", "sac", "vista"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		lines := splitLines(src)
		if len(lines) <= 2 {
			return nil // XINDEX guard: LC>2
		}
		if !firstLineSACok(lines[0]) {
			return []Finding{{
				Message: "First line of routine violates the SAC (expected `SITE/DEV-description`)",
				Line:    1, Col: 1, EndLine: 1, EndCol: 1,
			}}
		}
		return nil
	},
}

// --- command-keyword rules ---------------------------------------------------

func keywordRule(id, category, title string, sev Severity, tags []string,
	match func(kw string) bool, msg string) Rule {
	return Rule{
		ID: id, Severity: sev, Category: category, Title: title, Tags: tags,
		Inspect: func(root parse.Node, _ []byte) []Finding {
			var out []Finding
			forCommands(root, func(_, kwNode parse.Node, kw string) {
				if match(kw) {
					out = append(out, findNode(kwNode, msg))
				}
			})
			return out
		},
	}
}

// M-XINDX-020 — VIEW command used.
var ruleViewCommand = keywordRule("M-XINDX-020", "portability", "VIEW command used",
	Warning, []string{"xindex", "sac"},
	func(kw string) bool { return kw == "V" || kw == "VIEW" },
	"VIEW command used (non-portable; vendor-specific)")

// M-XINDX-025 — BREAK command used.
var ruleBreakCommand = keywordRule("M-XINDX-025", "bug", "BREAK command used",
	Warning, []string{"xindex", "sac"},
	func(kw string) bool { return kw == "B" || kw == "BREAK" },
	"BREAK command used (debug-only; should not appear in production code)")

// M-XINDX-029 — CLOSE should be invoked through D ^%ZISC.
var ruleCloseCommand = keywordRule("M-XINDX-029", "modernization", "CLOSE should be invoked through D ^%ZISC",
	Warning, []string{"xindex", "sac", "vista"},
	func(kw string) bool { return kw == "C" || kw == "CLOSE" },
	"CLOSE should be invoked through D ^%ZISC")

// M-XINDX-034 — OPEN should be invoked through ^%ZIS.
var ruleOpenCommand = keywordRule("M-XINDX-034", "modernization", "OPEN should be invoked through ^%ZIS",
	Warning, []string{"xindex", "sac", "vista"},
	func(kw string) bool { return kw == "O" || kw == "OPEN" },
	"OPEN should be invoked through ^%ZIS (portability across devices)")

// M-XINDX-036 — Should use TASKMAN instead of JOB.
var ruleJobCommand = keywordRule("M-XINDX-036", "modernization", "Should use TASKMAN instead of JOB",
	Warning, []string{"xindex", "sac", "vista"},
	func(kw string) bool { return kw == "J" || kw == "JOB" },
	"Should use TASKMAN instead of JOB command")

// M-XINDX-032 — HALT should be invoked through G ^XUSCLEAN. Unargumented H/HALT
// is HALT; H with an argument is HANG (not flagged).
var ruleHaltCommand = Rule{
	ID: "M-XINDX-032", Severity: Warning, Category: "modernization",
	Title: "HALT should be invoked through G ^XUSCLEAN", Tags: []string{"xindex", "sac", "vista"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if (kw == "H" || kw == "HALT") && !hasArgList(cmd) {
				out = append(out, findNode(kwNode, "HALT should be invoked through G ^XUSCLEAN"))
			}
		})
		return out
	},
}

// M-XINDX-002 — Non-standard 'Z' command.
var ruleZCommand = Rule{
	ID: "M-XINDX-002", Severity: Error, Category: "modernization",
	Title: "Non-standard 'Z' command", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(_, kwNode parse.Node, kw string) {
			if strings.HasPrefix(kw, "Z") && !standardCommands[kw] {
				out = append(out, findNode(kwNode, "Non-standard 'Z' command: "+kw))
			}
		})
		return out
	},
}

// M-XINDX-047 — Lowercase command(s) used in line. XINDEX-parity only (modern
// style prefers lowercase), so it is not in the modern profile.
var ruleLowercaseCommand = Rule{
	ID: "M-XINDX-047", Severity: Style, Category: "style",
	Title: "Lowercase command(s) used in line", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(_, kwNode parse.Node, _ string) {
			kw := string(kwNode.Text())
			if hasLowercaseLetter(kw) {
				out = append(out, findNode(kwNode,
					fmt.Sprintf("Lowercase command used: '%s' (XINDEX style; modern profiles often allow this)", kw)))
			}
		})
		return out
	},
}

// --- KILL / NEW / LOCK / READ / SET command-shape rules ----------------------

// isExclusiveList reports whether an argument payload is an exclusive
// variable list — `(A,B)` parses as set_target_list, `(A)` as parenthesized.
// (The Python tool only checked set_target_list and so missed the single-var
// form; both are exclusive, so the Go port flags both.)
func isExclusiveList(payload parse.Node) bool {
	t := payload.Type()
	return t == "set_target_list" || t == "parenthesized"
}

// M-XINDX-022 — Exclusive Kill.
var ruleExclusiveKill = Rule{
	ID: "M-XINDX-022", Severity: Style, Category: "style",
	Title: "Exclusive Kill", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if kw != "K" && kw != "KILL" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				if p, ok := argPayload(arg); ok && isExclusiveList(p) {
					out = append(out, findNode(kwNode, "Exclusive KILL — KILL (var,…) is non-standard / dangerous"))
					break
				}
			}
		})
		return out
	},
}

// M-XINDX-023 — Unargumented Kill.
var ruleUnargumentedKill = Rule{
	ID: "M-XINDX-023", Severity: Warning, Category: "bug",
	Title: "Unargumented Kill", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if (kw == "K" || kw == "KILL") && !hasArgList(cmd) {
				out = append(out, findNode(kwNode, "Unargumented KILL — kills all locals; almost never what is intended"))
			}
		})
		return out
	},
}

// M-XINDX-024 — Kill of an unsubscripted global.
var ruleKillUnsubGlobal = Rule{
	ID: "M-XINDX-024", Severity: Error, Category: "bug",
	Title: "Kill of an unsubscripted global", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			if kw != "K" && kw != "KILL" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				p, ok := argPayload(arg)
				if !ok || p.Type() != "variable" {
					continue
				}
				gv, ok := childType(p, "global_variable")
				if !ok {
					continue
				}
				if _, hasSubs := childType(gv, "subscripts"); !hasSubs {
					out = append(out, findNode(gv, "Kill of an unsubscripted global (kills the entire global tree)"))
				}
			}
		})
		return out
	},
}

// M-XINDX-026 — Exclusive or Unargumented NEW command.
var ruleNewExclUnarg = Rule{
	ID: "M-XINDX-026", Severity: Style, Category: "style",
	Title: "Exclusive or Unargumented NEW command", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if kw != "N" && kw != "NEW" {
				return
			}
			if !hasArgList(cmd) {
				out = append(out, findNode(kwNode, "Unargumented NEW (news everything; non-standard intent)"))
				return
			}
			for _, arg := range cmdArguments(cmd) {
				if p, ok := argPayload(arg); ok && isExclusiveList(p) {
					out = append(out, findNode(kwNode, "Exclusive NEW — NEW (var,…) is non-standard"))
					break
				}
			}
		})
		return out
	},
}

// M-XINDX-033 — READ command does not have a timeout.
var ruleReadNoTimeout = Rule{
	ID: "M-XINDX-033", Severity: Warning, Category: "bug",
	Title: "READ command does not have a timeout", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if kw != "R" && kw != "READ" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				// Only a variable read (`R X`) blocks and needs a :timeout. Format
				// controls (`!`, `#`, `?n`), string prompts, and `*X`/`#` reads are
				// not blocking variable reads — XINDEX doesn't flag them either.
				p, ok := argPayload(arg)
				if !ok || p.Type() != "variable" {
					continue
				}
				if !argHasTimeout(arg) {
					out = append(out, findNode(kwNode, "READ command does not have a :timeout (will block indefinitely)"))
					break
				}
			}
		})
		return out
	},
}

// M-XINDX-060 — LOCK missing timeout.
var ruleLockNoTimeout = Rule{
	ID: "M-XINDX-060", Severity: Warning, Category: "concurrency",
	Title: "LOCK missing timeout", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, kwNode parse.Node, kw string) {
			if kw != "L" && kw != "LOCK" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				// A lock RELEASE (`L -^X`) never blocks, so it needs no timeout —
				// only acquires (`L ^X` / `L +^X`) do. XINDEX flags acquires only.
				p, ok := argPayload(arg)
				if ok && p.Type() == "unary_expression" {
					if op, ok2 := childType(p, "operator"); ok2 && string(op.Text()) == "-" {
						continue
					}
				}
				if !argHasTimeout(arg) {
					out = append(out, findNode(kwNode, "LOCK missing :timeout (will block indefinitely)"))
					break
				}
			}
		})
		return out
	},
}

// M-XINDX-061 — Non-incremental LOCK (no +/-).
var ruleNonIncrementalLock = Rule{
	ID: "M-XINDX-061", Severity: Warning, Category: "concurrency",
	Title: "Non-incremental LOCK", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			if kw != "L" && kw != "LOCK" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				p, ok := argPayload(arg)
				if !ok {
					continue
				}
				if p.Type() == "unary_expression" {
					if op, ok := childType(p, "operator"); ok {
						if t := string(op.Text()); t == "+" || t == "-" {
							continue // incremental ⇒ ok
						}
					}
				}
				out = append(out, findNode(p, "Non-incremental LOCK — releases all prior locks; use `LOCK +var`"))
				break
			}
		})
		return out
	},
}

// M-XINDX-041 — Star or pound READ used.
var ruleStarPoundRead = Rule{
	ID: "M-XINDX-041", Severity: Info, Category: "modernization",
	Title: "Star or pound READ used", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			if kw != "R" && kw != "READ" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				p, ok := argPayload(arg)
				if !ok || p.Type() != "unary_expression" {
					continue
				}
				op, ok := childType(p, "operator")
				if !ok {
					continue
				}
				if t := string(op.Text()); t == "*" || t == "#" {
					out = append(out, findNode(p, fmt.Sprintf("Star or pound READ used (R%s…)", t)))
				}
			}
		})
		return out
	},
}

// M-XINDX-045 — Set to a '%' global.
var ruleSetPercentGlobal = Rule{
	ID: "M-XINDX-045", Severity: Warning, Category: "bug",
	Title: "Set to a '%' global", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			if kw != "S" && kw != "SET" {
				return
			}
			for _, arg := range cmdArguments(cmd) {
				p, ok := argPayload(arg)
				if !ok || p.Type() != "binary_expression" || p.ChildCount() == 0 {
					continue
				}
				lhs := p.Child(0)
				if lhs.Type() != "variable" {
					continue
				}
				gv, ok := childType(lhs, "global_variable")
				if !ok {
					continue
				}
				id, ok := childType(gv, "identifier")
				if !ok {
					continue
				}
				name := string(id.Text())
				if strings.HasPrefix(name, "%") {
					out = append(out, findNode(gv, fmt.Sprintf("Set to a '%%' global (^%s); reserved for system use", name)))
				}
			}
		})
		return out
	},
}

// M-XINDX-030 — LABEL+OFFSET syntax in a DO/GOTO/JOB argument.
var ruleLabelOffset = Rule{
	ID: "M-XINDX-030", Severity: Warning, Category: "bug",
	Title: "LABEL+OFFSET syntax", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			switch kw {
			case "D", "DO", "G", "GOTO", "J", "JOB":
			default:
				return
			}
			for _, arg := range cmdArguments(cmd) {
				// Only the entry reference itself being `TAG+offset` is label+offset:
				// the arg's TOP-LEVEL payload is a binary_expression `+` whose LHS is a
				// label-shaped variable. Do NOT walk descendants — `D UP((I+2),...)` is
				// argument arithmetic, not a label offset (the dominant false positive).
				p, ok := argPayload(arg)
				if !ok || p.Type() != "binary_expression" || p.ChildCount() == 0 {
					continue
				}
				// LHS must be a label: a named label (`TAG+1` → variable) or a
				// numeric label (`33+1` → number). Argument arithmetic like
				// `D UP((I+2),...)` has a non-binary_expression top-level payload.
				if lhs := p.Child(0).Type(); lhs != "variable" && lhs != "number" {
					continue
				}
				if op, ok := childType(p, "operator"); ok && string(op.Text()) == "+" {
					out = append(out, findNode(p, "LABEL+OFFSET syntax — offset-dependent calls are fragile"))
				}
			}
		})
		return out
	},
}

// --- special-variable / intrinsic-function rules -----------------------------

// M-XINDX-027 — $VIEW function used.
var ruleDollarView = Rule{
	ID: "M-XINDX-027", Severity: Warning, Category: "portability",
	Title: "$View function used", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "function_call" {
				return
			}
			kw, ok := childType(n, "intrinsic_function_keyword")
			if !ok {
				return
			}
			name := strings.ToUpper(string(kw.Text()))
			if name == "$V" || name == "$VIEW" {
				out = append(out, findNode(kw, "$VIEW function used (non-portable; vendor-specific)"))
			}
		})
		return out
	},
}

// M-XINDX-028 — Non-standard $Z special variable.
var ruleNonStandardZSV = Rule{
	ID: "M-XINDX-028", Severity: Warning, Category: "portability",
	Title: "Non-standard $Z special variable", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "special_variable" {
				return
			}
			text := strings.ToUpper(string(n.Text()))
			if !strings.HasPrefix(text, "$Z") {
				return
			}
			name := reLeadingZ.FindString(text)
			if name == "" || standardISVs[name] {
				return
			}
			s := n.StartPoint()
			out = append(out, Finding{
				Message: "Non-standard $Z special variable: " + name,
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1,
				EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1 + len(name),
			})
		})
		return out
	},
}

// M-XINDX-031 — Non-standard $Z function.
var ruleNonStandardZFunc = Rule{
	ID: "M-XINDX-031", Severity: Warning, Category: "portability",
	Title: "Non-standard $Z function", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "function_call" {
				return
			}
			text := strings.ToUpper(string(n.Text()))
			name := reLeadingZ.FindString(text)
			if name == "" || standardFunctions[name] {
				return
			}
			s := n.StartPoint()
			out = append(out, Finding{
				Message: "Non-standard $Z function: " + name,
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1,
				EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1 + len(name),
			})
		})
		return out
	},
}

// M-XINDX-054 — Access to SSVN's or $SYSTEM restricted to Kernel. Covers both
// halves of the XINDEX rule: (1) `$SYSTEM` (incl. the `$SYSTEM.Class.Method`
// object syntax, whose keyword lands under an ERROR node since the grammar
// doesn't model object refs — so we match special_variable_keyword directly
// rather than a clean special_variable parent); (2) SSVNs `^$JOB`, `^$ROUTINE`,
// `^$R`, … — a global whose name begins with `$`.
var ruleSystemAccess = Rule{
	ID: "M-XINDX-054", Severity: Warning, Category: "security",
	Title: "Access to SSVN's or $SYSTEM restricted to Kernel", Tags: []string{"xindex", "sac", "vista"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			switch n.Type() {
			case "special_variable_keyword":
				if name := strings.ToUpper(string(n.Text())); name == "$SY" || name == "$SYSTEM" {
					out = append(out, findNode(n, "$SYSTEM access — restricted to Kernel package"))
				}
			case "global_variable":
				if bytes.HasPrefix(n.Text(), []byte("^$")) {
					out = append(out, findNode(n, "SSVN access (^$...) — structured system variables are restricted to Kernel"))
				}
			}
		})
		return out
	},
}

// M-XINDX-050 — Extended reference (UCI/namespace-bound global).
var ruleExtendedReference = Rule{
	ID: "M-XINDX-050", Severity: Warning, Category: "portability",
	Title: "Extended reference", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "global_variable" {
				return
			}
			// The extended-reference marker (`^|env|gvn` / `^[...]gvn`) is in the
			// NAME, before any subscripts. A `|`/`[` inside a subscript string
			// (e.g. ^TMP("X",Y_" | Z |")) is not an extended reference — checking
			// the whole node text was the dominant false positive.
			name := n.Text()
			if i := bytes.IndexByte(name, '('); i >= 0 {
				name = name[:i]
			}
			if bytes.ContainsAny(name, "|[") {
				out = append(out, findNode(n, "Extended reference — UCI/namespace-bound calls reduce portability"))
			}
		})
		return out
	},
}

// --- label / structure rules -------------------------------------------------

// M-XINDX-015 — Duplicate label.
var ruleDuplicateLabel = Rule{
	ID: "M-XINDX-015", Severity: Warning, Category: "bug",
	Title: "Duplicate label", Tags: []string{"xindex"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		type pos struct{ line, col, end int }
		seen := map[string][]pos{}
		var order []string
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "label" {
				return
			}
			name := string(n.Text())
			s, e := n.StartPoint(), n.EndPoint()
			if _, ok := seen[name]; !ok {
				order = append(order, name)
			}
			seen[name] = append(seen[name], pos{int(s.Row) + 1, int(s.Column) + 1, int(e.Column) + 1})
		})
		var out []Finding
		for _, name := range order {
			occ := seen[name]
			if len(occ) <= 1 {
				continue
			}
			for _, p := range occ[1:] {
				out = append(out, Finding{
					Message: fmt.Sprintf("Duplicate label: '%s' (first defined at line %d)", name, occ[0].line),
					Line:    p.line, Col: p.col, EndLine: p.line, EndCol: p.end,
				})
			}
		}
		return out
	},
}

// M-XINDX-017 — First line label NOT routine name. Name-aware: needs the routine
// name (file base without extension). Routines whose name starts with % are
// exempt (XINDEX exclusion). No-ops when the routine name is unknown.
var ruleFirstLabelName = Rule{
	ID: "M-XINDX-017", Severity: Warning, Category: "bug",
	Title: "First line label NOT routine name", Tags: []string{"xindex", "sac"},
	InspectNamed: func(root parse.Node, _ []byte, routine string) []Finding {
		if routine == "" || strings.HasPrefix(routine, "%") {
			return nil
		}
		var first parse.Node
		found := false
		walkNodes(root, func(n parse.Node) {
			if !found && n.Type() == "label" {
				first, found = n, true
			}
		})
		if !found {
			return nil
		}
		name := string(first.Text())
		if name == routine {
			return nil
		}
		s, e := first.StartPoint(), first.EndPoint()
		return []Finding{{
			Message: fmt.Sprintf("First line label ('%s') does not match routine name ('%s')", name, routine),
			Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
		}}
	},
}

// M-XINDX-014 — Call to missing label in this routine. Name-aware: the routine
// name disambiguates `label^routine` self-references; bare-label (`D TAG`) and
// no-routine extrinsic (`$$F()`) calls are checked even when the name is unknown.
var ruleMissingLabelCall = Rule{
	ID: "M-XINDX-014", Severity: Error, Category: "bug",
	Title: "Call to missing label in this routine", Tags: []string{"xindex"},
	InspectNamed: func(root parse.Node, _ []byte, routine string) []Finding {
		labels := map[string]bool{}
		walkNodes(root, func(n parse.Node) {
			if n.Type() == "label" {
				labels[string(n.Text())] = true
			}
		})
		var out []Finding
		emit := func(node parse.Node, name string) {
			if labels[name] {
				return
			}
			s, e := node.StartPoint(), node.EndPoint()
			out = append(out, Finding{
				Message: fmt.Sprintf("Call to missing label '%s' in this routine", name),
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
			})
		}
		forCommands(root, func(cmd, _ parse.Node, kw string) {
			switch kw {
			case "D", "DO", "G", "GOTO", "J", "JOB":
			default:
				return
			}
			for _, arg := range cmdArguments(cmd) {
				p, ok := argPayload(arg)
				if !ok {
					continue
				}
				switch p.Type() {
				case "entry_reference":
					if node, name, ok := labelFromEntryRef(p, routine); ok {
						emit(node, name)
					}
				case "variable":
					lv, ok := childType(p, "local_variable")
					if !ok {
						continue
					}
					if id, ok := childType(lv, "identifier"); ok {
						emit(id, string(id.Text()))
					}
				}
			}
		})
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "extrinsic_function" {
				return
			}
			if node, name, ok := labelFromExtrinsic(n, routine); ok {
				emit(node, name)
			}
		})
		return out
	},
}

// labelFromEntryRef extracts the in-routine label node + name from a
// `label^routine` / `^routine` entry_reference, or returns ok=false when the
// reference is cross-routine, a `^routine` form, or a numeric label.
func labelFromEntryRef(ref parse.Node, routine string) (parse.Node, string, bool) {
	var children []parse.Node
	caretIdx := -1
	for i := uint32(0); i < ref.ChildCount(); i++ {
		c := ref.Child(i)
		children = append(children, c)
		if c.Type() == "^" {
			caretIdx = len(children) - 1
		}
	}
	var labelNode parse.Node
	if caretIdx >= 0 {
		if caretIdx+1 < len(children) {
			if string(children[caretIdx+1].Text()) != routine {
				return parse.Node{}, "", false // cross-routine
			}
		}
		if caretIdx == 0 {
			return parse.Node{}, "", false // ^routine form
		}
		labelNode = children[caretIdx-1]
	} else {
		if len(children) == 0 {
			return parse.Node{}, "", false
		}
		labelNode = children[0]
	}
	if labelNode.Type() != "identifier" {
		return parse.Node{}, "", false // numeric label — out of scope
	}
	return labelNode, string(labelNode.Text()), true
}

// labelFromExtrinsic extracts the label node + name from a `$$label` /
// `$$label^routine` extrinsic_function, or ok=false when cross-routine.
func labelFromExtrinsic(fn parse.Node, routine string) (parse.Node, string, bool) {
	var ids []parse.Node
	hasCaret := false
	for i := uint32(0); i < fn.ChildCount(); i++ {
		c := fn.Child(i)
		switch c.Type() {
		case "identifier":
			ids = append(ids, c)
		case "^":
			hasCaret = true
		}
	}
	if len(ids) < 1 {
		return parse.Node{}, "", false
	}
	if hasCaret && len(ids) >= 2 {
		if string(ids[1].Text()) != routine {
			return parse.Node{}, "", false
		}
	}
	return ids[0], string(ids[0].Text()), true
}

// M-XINDX-057 — Lower/mixed case in local variable name.
var ruleLocalVarCase = Rule{
	ID: "M-XINDX-057", Severity: Style, Category: "style",
	Title: "Lower/mixed case in local variable name", Tags: []string{"xindex", "sac"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		seen := map[string]bool{}
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if n.Type() != "local_variable" {
				return
			}
			id, ok := childType(n, "identifier")
			if !ok {
				return
			}
			name := string(id.Text())
			if !hasLowercaseLetter(name) {
				return
			}
			s := id.StartPoint()
			key := fmt.Sprintf("%d:%d:%s", s.Row, s.Column, name)
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, Finding{
				Message: fmt.Sprintf("Lower/mixed case in local variable name: '%s'", name),
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1 + len(name),
			})
		})
		return out
	},
}

// --- parse-error + control-flow rules ----------------------------------------

// M-XINDX-021 — General syntax error (tree-sitter ERROR / MISSING nodes).
var ruleSyntaxError = Rule{
	ID: "M-XINDX-021", Severity: Error, Category: "bug",
	Title: "General syntax error", Tags: []string{"xindex"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		if !root.HasError() {
			return nil
		}
		var out []Finding
		walkNodes(root, func(n parse.Node) {
			if !n.IsError() && !n.IsMissing() {
				return
			}
			s, e := n.StartPoint(), n.EndPoint()
			endCol := int(e.Column) + 1
			if endCol <= int(s.Column)+1 {
				endCol = int(s.Column) + 2
			}
			msg := "General syntax error"
			if n.IsMissing() {
				msg += " (missing token)"
			}
			out = append(out, Finding{
				Message: msg,
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(e.Row) + 1, EndCol: endCol,
			})
		})
		return out
	},
}

var terminatingKeywords = map[string]bool{"Q": true, "QUIT": true, "H": true, "HALT": true, "G": true, "GOTO": true}

// M-XINDX-009 — Unreachable code after an unconditional QUIT / HALT / GOTO.
var ruleDeadCodeAfterQuit = Rule{
	ID: "M-XINDX-009", Severity: Warning, Category: "bug",
	Title: "Unreachable code after unconditional QUIT / HALT / GOTO", Tags: []string{"xindex"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		terminatedAt := 0 // 0 = not terminated in the current label
		for _, line := range topLevelLines(root) {
			if _, ok := childType(line, "label"); ok {
				terminatedAt = 0
				continue
			}
			if _, ok := childType(line, "dot_block_prefix"); ok {
				continue
			}
			cs, ok := childType(line, "command_sequence")
			if !ok {
				continue
			}
			cmds := commandsOf(cs)
			if len(cmds) == 0 {
				continue
			}
			lineNo := int(line.StartPoint().Row) + 1
			if terminatedAt != 0 {
				out = append(out, Finding{
					Message: fmt.Sprintf("Unreachable code: line follows an unconditional terminator on line %d", terminatedAt),
					Line:    lineNo, Col: 1, EndLine: lineNo, EndCol: 1,
				})
				continue
			}
			first := cmds[0]
			_, kw, ok := cmdKeyword(first)
			if !ok || !terminatingKeywords[kw] {
				continue
			}
			if cmdHasPostcond(first) {
				continue
			}
			terminatedAt = lineNo
		}
		return out
	},
}

var conditionalKeywords = map[string]bool{"I": true, "IF": true, "E": true, "ELSE": true}

// M-XINDX-051 — IF / ELSE with no body on the same line.
var ruleEmptyConditional = Rule{
	ID: "M-XINDX-051", Severity: Warning, Category: "bug",
	Title: "IF / ELSE with no body on the same line", Tags: []string{"xindex"},
	Inspect: func(root parse.Node, _ []byte) []Finding {
		var out []Finding
		for _, line := range topLevelLines(root) {
			cs, ok := childType(line, "command_sequence")
			if !ok {
				continue
			}
			cmds := commandsOf(cs)
			if len(cmds) != 1 {
				continue
			}
			kwNode, kw, ok := cmdKeyword(cmds[0])
			if !ok || !conditionalKeywords[kw] {
				continue
			}
			out = append(out, findNode(kwNode,
				fmt.Sprintf("%s has no body on the same line — M conditionals only gate commands that follow on the SAME line", kw)))
		}
		return out
	},
}

func commandsOf(cs parse.Node) []parse.Node {
	var out []parse.Node
	for i := uint32(0); i < cs.ChildCount(); i++ {
		if c := cs.Child(i); c.Type() == "command" {
			out = append(out, c)
		}
	}
	return out
}
