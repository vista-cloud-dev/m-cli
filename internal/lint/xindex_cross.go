package lint

import (
	"fmt"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/workspace"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// Cross-routine XINDEX rules (M-XINDX-007/008/049) — faithful ports of the
// Python tool's needs_context rules. They run only when a workspace index is
// attached (see Linter.AttachWorkspace); without it they are skipped, so plain
// single-file `Lint` and the editor/watch paths never fire them spuriously.

// M-XINDX-007 — Call to undefined routine. For each outbound reference whose
// target routine is NOT this routine and NOT in the workspace (and not in the
// trusted-routine allowlist), flag it. Bare-label and intra-routine refs are
// M-XINDX-014/008's job. The trusted allowlist is baked in from [lint.vista]
// trusted_routines (nil ⇒ strict).
func ruleUndefinedRoutine(trusted map[string]bool) Rule {
	return Rule{
		ID: "M-XINDX-007", Severity: Error, Category: "bug",
		Title: "Call to undefined routine", Tags: []string{"xindex"},
		InspectWorkspace: func(root parse.Node, _ []byte, routine string, ws *workspace.Index) []Finding {
			this := strings.ToUpper(routine)
			var out []Finding
			seen := map[string]bool{}
			for _, ref := range workspace.References(root, routine) {
				if ref.TargetRoutine == this || ws.HasRoutine(ref.TargetRoutine) {
					continue
				}
				if trusted[ref.TargetRoutine] {
					continue
				}
				if seen[ref.TargetRoutine] {
					continue // one finding per missing routine
				}
				seen[ref.TargetRoutine] = true
				out = append(out, Finding{
					Message: fmt.Sprintf("call to undefined routine ^%s", ref.TargetRoutine),
					Line:    ref.Line, Col: ref.Col + 1, EndLine: ref.Line, EndCol: ref.EndCol + 1,
				})
			}
			return out
		},
	}
}

// M-XINDX-008 — Call to undefined label in another routine. For each
// LABEL^ROUTINE reference where ROUTINE is indexed but LABEL doesn't exist in
// it, flag it. Skips `^ROUTINE` (no label), intra-routine refs (M-XINDX-014's
// job), and refs to routines outside the workspace (M-XINDX-007's job).
var ruleUndefinedLabel = Rule{
	ID: "M-XINDX-008", Severity: Error, Category: "bug",
	Title: "Call to undefined label in another routine", Tags: []string{"xindex"},
	InspectWorkspace: func(root parse.Node, _ []byte, routine string, ws *workspace.Index) []Finding {
		this := strings.ToUpper(routine)
		var out []Finding
		seen := map[string]bool{}
		for _, ref := range workspace.References(root, routine) {
			if ref.TargetLabel == "" || ref.TargetRoutine == this {
				continue
			}
			if !ws.HasRoutine(ref.TargetRoutine) || ws.Lookup(ref.TargetRoutine, ref.TargetLabel) {
				continue
			}
			key := ref.TargetLabel + "^" + ref.TargetRoutine
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Finding{
				Message: fmt.Sprintf("call to undefined label %s^%s", ref.TargetLabel, ref.TargetRoutine),
				Line:    ref.Line, Col: ref.Col + 1, EndLine: ref.Line, EndCol: ref.EndCol + 1,
			})
		}
		return out
	},
}

// M-XINDX-049 — Label declared but never referenced anywhere in the workspace.
// The routine-entry label (name == routine) is exempt (callable as D ^ROUTINE).
// Routines using runtime label dispatch ($TEXT, D @var, ^DD/^DIC xref tables)
// are skipped entirely — their label graph is dynamic. STYLE (a hygiene smell).
var ruleLabelNeverReferenced = Rule{
	ID: "M-XINDX-049", Severity: Style, Category: "style",
	Title: "Label declared but never referenced", Tags: []string{"xindex"},
	InspectWorkspace: func(root parse.Node, src []byte, routine string, ws *workspace.Index) []Finding {
		if workspace.UsesRuntimeLabelLookup(src) {
			return nil
		}
		this := strings.ToUpper(routine)
		var out []Finding
		for _, lbl := range workspace.Labels(root) {
			if strings.ToUpper(lbl.Name) == this {
				continue // routine entry — exempt
			}
			if ws.ReferencesTo(routine, lbl.Name) > 0 {
				continue
			}
			out = append(out, Finding{
				Message: fmt.Sprintf("label %q is declared but never referenced", lbl.Name),
				Line:    lbl.Line, Col: 1, EndLine: lbl.Line, EndCol: 1 + len(lbl.Name),
			})
		}
		return out
	},
}
