package mcov

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// DiscoverExecutables walks each routine file and returns its executable lines.
// A line is executable iff its `line` AST node has a `command_sequence` child
// (comment-only and label-only lines are excluded — YDB's TRACE emits no hits
// for them, so their absence never means "uncovered"). Each line carries the
// owning label and its YDB trace offset = lineNumber − labelDeclarationLine.
func DiscoverExecutables(p *parse.Parser, routinePaths []string) ([]ExecLine, error) {
	var out []ExecLine
	for _, path := range routinePaths {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		ls, err := executableLines(p, path, src)
		if err != nil {
			return nil, err
		}
		out = append(out, ls...)
	}
	return out, nil
}

func executableLines(p *parse.Parser, path string, src []byte) ([]ExecLine, error) {
	tree, err := p.Parse(context.Background(), src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	routine := strings.ToUpper(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	var out []ExecLine
	curLabel := ""
	curLabelLine := 0
	root := tree.RootNode()
	for i := uint32(0); i < root.NamedChildCount(); i++ {
		line := root.NamedChild(i)
		if line.Type() != "line" {
			continue
		}
		lineNo := int(line.StartPoint().Row) + 1
		var hasCmd bool
		for j := uint32(0); j < line.ChildCount(); j++ {
			switch line.Child(j).Type() {
			case "label":
				curLabel = string(line.Child(j).Text())
				curLabelLine = lineNo
			case "command_sequence":
				hasCmd = true
			}
		}
		if !hasCmd || curLabel == "" {
			continue
		}
		out = append(out, ExecLine{
			Routine: routine, Label: curLabel, Path: path,
			Line: lineNo, Offset: lineNo - curLabelLine,
		})
	}
	return out, nil
}
