// Package workspace is an in-memory cross-routine index of M source: every
// routine's labels (declarations) and outbound call sites (references). It is
// the substrate the cross-routine lint rules need (M-XINDX-007 call-to-undefined
// -routine, 008 call-to-undefined-label, 049 label-never-referenced) — the
// single-file linter has no other way to resolve LABEL^ROUTINE across files.
// Faithful port of the Python tool's m_cli.workspace.WorkspaceIndex.
//
// Routine identity comes from the file base name (upper-cased), matching ydb's
// resolution — not the first-label-equals-routine convention, which not every
// codebase follows.
package workspace

import (
	"regexp"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Label is one labeled entry point.
type Label struct {
	Routine string // upper-case routine name
	Name    string // original-case label
	Line    int    // 1-based
}

// Reference is one outbound call site. TargetLabel is "" for `^ROUTINE` /
// `$$^ROUTINE` forms that name no label. Positions are 0-based columns.
type Reference struct {
	TargetRoutine string // upper
	TargetLabel   string // upper, "" when only ^ROUTINE was written
	Line          int    // 1-based
	Col           int    // 0-based
	EndCol        int    // 0-based exclusive
}

// Index holds labels keyed by routine and references keyed by target.
type Index struct {
	byRoutine    map[string][]Label
	refsByTarget map[string][]Reference
}

func New() *Index {
	return &Index{byRoutine: map[string][]Label{}, refsByTarget: map[string][]Reference{}}
}

func targetKey(routine, label string) string { return routine + "\x00" + label }

// AddFile indexes one parsed file's labels + references. routine is the file
// base name (without extension); it is upper-cased internally.
func (i *Index) AddFile(routine string, root parse.Node) {
	ru := strings.ToUpper(routine)
	for _, l := range Labels(root) {
		l.Routine = ru
		i.byRoutine[ru] = append(i.byRoutine[ru], l)
	}
	for _, r := range References(root, routine) {
		k := targetKey(r.TargetRoutine, r.TargetLabel)
		i.refsByTarget[k] = append(i.refsByTarget[k], r)
	}
}

// HasRoutine reports whether any label is indexed for routine (case-insensitive).
func (i *Index) HasRoutine(routine string) bool {
	_, ok := i.byRoutine[strings.ToUpper(routine)]
	return ok
}

// Lookup reports whether LABEL^ROUTINE resolves. label "" means the routine
// entry (resolves iff the routine has any label, per M semantics).
func (i *Index) Lookup(routine, label string) bool {
	entries := i.byRoutine[strings.ToUpper(routine)]
	if len(entries) == 0 {
		return false
	}
	if label == "" {
		return true
	}
	target := strings.ToUpper(label)
	for _, l := range entries {
		if strings.ToUpper(l.Name) == target {
			return true
		}
	}
	return false
}

// ReferencesTo counts indexed call sites whose target matches (routine, label).
func (i *Index) ReferencesTo(routine, label string) int {
	return len(i.refsByTarget[targetKey(strings.ToUpper(routine), strings.ToUpper(label))])
}

// --- extraction (also used by the cross-routine rules on the current file) ---

// Labels returns each top-level line's first label.
func Labels(root parse.Node) []Label {
	var out []Label
	for i := uint32(0); i < root.ChildCount(); i++ {
		line := root.Child(i)
		if line.Type() != "line" {
			continue
		}
		for j := uint32(0); j < line.ChildCount(); j++ {
			if c := line.Child(j); c.Type() == "label" {
				s := c.StartPoint()
				out = append(out, Label{Name: string(c.Text()), Line: int(s.Row) + 1})
				break
			}
		}
	}
	return out
}

// callHeader matches the call header at the start of a reference's text:
// LABEL^ROUTINE / LABEL / ^ROUTINE (after stripping a leading $$).
var callHeader = regexp.MustCompile(`^([%A-Za-z][A-Za-z0-9]*)?(\^([%A-Za-z][A-Za-z0-9]*))?`)

var labelCallKeywords = map[string]bool{"D": true, "DO": true, "G": true, "GOTO": true, "J": true, "JOB": true}

// References extracts every cross-routine call site: entry_reference /
// extrinsic_function nodes (via the call-header regex) plus bare-label
// DO/GOTO/JOB arguments (which target a label in the current routine).
func References(root parse.Node, routine string) []Reference {
	var out []Reference
	walk(root, func(n parse.Node) {
		switch n.Type() {
		case "entry_reference", "extrinsic_function":
			if r, ok := refFromCallNode(n, routine); ok {
				out = append(out, r)
			}
		case "command":
			out = append(out, bareLabelRefs(n, routine)...)
		}
	})
	return out
}

func refFromCallNode(n parse.Node, routine string) (Reference, bool) {
	text := string(n.Text())
	offset := 0
	if n.Type() == "extrinsic_function" && strings.HasPrefix(text, "$$") {
		text = text[2:]
		offset = 2
	}
	m := callHeader.FindStringSubmatch(text)
	if m == nil || (m[1] == "" && m[3] == "") {
		return Reference{}, false
	}
	label, rtn := m[1], m[3]
	if rtn == "" {
		rtn = routine
	}
	s := n.StartPoint()
	startCol := int(s.Column) + offset
	tl := ""
	if label != "" {
		tl = strings.ToUpper(label)
	}
	return Reference{
		TargetRoutine: strings.ToUpper(rtn),
		TargetLabel:   tl,
		Line:          int(s.Row) + 1,
		Col:           startCol,
		EndCol:        startCol + len(m[0]),
	}, true
}

func bareLabelRefs(cmd parse.Node, routine string) []Reference {
	kwNode, ok := childOf(cmd, "command_keyword")
	if !ok || !labelCallKeywords[strings.ToUpper(string(kwNode.Text()))] {
		return nil
	}
	al, ok := childOf(cmd, "argument_list")
	if !ok {
		return nil
	}
	var out []Reference
	for i := uint32(0); i < al.ChildCount(); i++ {
		arg := al.Child(i)
		if arg.Type() != "argument" {
			continue
		}
		v, ok := simpleVariable(arg)
		if !ok {
			continue
		}
		id, ok := childOf(v, "identifier")
		if !ok {
			continue
		}
		s, e := arg.StartPoint(), arg.EndPoint()
		ref := Reference{
			Line:   int(s.Row) + 1,
			Col:    int(s.Column),
			EndCol: int(e.Column),
		}
		switch v.Type() {
		case "local_variable":
			// `D LABEL` — a bare label call in the current routine.
			ref.TargetRoutine = strings.ToUpper(routine)
			ref.TargetLabel = strings.ToUpper(string(id.Text()))
		case "global_variable":
			// `D ^ROUTINE` / `D ^ROUTINE(args)` — a routine-entry call. The
			// grammar parses `^name` as a global_variable; in a DO/GOTO/JOB
			// argument it is a routine reference with no label.
			ref.TargetRoutine = strings.ToUpper(string(id.Text()))
		default:
			continue
		}
		out = append(out, ref)
	}
	return out
}

// simpleVariable returns the local_variable or global_variable iff arg is
// exactly argument → variable → (local|global)_variable — filtering out
// indirection / expressions that aren't bare label / routine calls.
func simpleVariable(arg parse.Node) (parse.Node, bool) {
	named := namedChildren(arg)
	if len(named) != 1 || named[0].Type() != "variable" {
		return parse.Node{}, false
	}
	vc := namedChildren(named[0])
	if len(vc) != 1 {
		return parse.Node{}, false
	}
	if t := vc[0].Type(); t == "local_variable" || t == "global_variable" {
		return vc[0], true
	}
	return parse.Node{}, false
}

// runtimeLabelLookupMarkers signal a routine reaches labels via runtime
// introspection / indirection beyond static analysis (used by M-XINDX-049).
var runtimeLabelLookupMarkers = [][]byte{
	[]byte("$TEXT("), []byte("$T("), []byte(" @"), []byte("\t@"),
	[]byte("^DD("), []byte("^DIC("), []byte("^XOBV"), []byte("^ORD("),
}

// UsesRuntimeLabelLookup is a coarse, conservative check: if any marker is
// present, the routine's label graph may be dynamic, so M-XINDX-049 skips it.
func UsesRuntimeLabelLookup(src []byte) bool {
	for _, m := range runtimeLabelLookupMarkers {
		if bytesContains(src, m) {
			return true
		}
	}
	return false
}

// --- small node helpers (workspace-local; lint imports this package) ---

func walk(n parse.Node, fn func(parse.Node)) {
	fn(n)
	for i := uint32(0); i < n.ChildCount(); i++ {
		walk(n.Child(i), fn)
	}
}

func childOf(n parse.Node, typ string) (parse.Node, bool) {
	for i := uint32(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Type() == typ {
			return c, true
		}
	}
	return parse.Node{}, false
}

func namedChildren(n parse.Node) []parse.Node {
	var out []parse.Node
	for i := uint32(0); i < n.NamedChildCount(); i++ {
		out = append(out, n.NamedChild(i))
	}
	return out
}

func bytesContains(haystack, needle []byte) bool {
	return strings.Contains(string(haystack), string(needle))
}
