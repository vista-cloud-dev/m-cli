// Package flow is m-cli's control-flow / dataflow infrastructure for the
// path-sensitive lint rules (spec §3.1; the Python tool's "Phase 7"). It builds
// a per-label control-flow graph over the m-parse tree and provides dataflow
// passes (currently LOCK-held state, driving M-MOD-025). The CFG is built
// structurally — walking top-level `line` nodes and attaching each command's
// dot-block flag — so it needs no parent pointers from the parser.
//
// Faithful to the reference model: per-label CFG with an entry block, one block
// per `command` node in source order, and an exit block; edges are
// fall/branch/skip/exit/if-skip, with QUIT inside a dot-block modeled as a
// dot-block exit (fall-through) rather than a label exit.
package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Block is a node in a per-label CFG. Succ and Edges are parallel.
type Block struct {
	ID      int
	Kind    string // "entry" | "command" | "exit"
	Command parse.Node
	HasCmd  bool
	Succ    []int
	Edges   []string
	Line    int // 1-based
}

// CFG is one label's control-flow graph. Block 0 is the entry; the last block
// is the exit.
type CFG struct {
	LabelName string
	LabelRow  int // 0-based
	LabelCol  int // 0-based
	Blocks    []Block
}

// ExitID is the id of the exit block.
func (c CFG) ExitID() int { return len(c.Blocks) - 1 }

var (
	exitKW = map[string]bool{"Q": true, "QUIT": true, "H": true, "HALT": true, "G": true, "GOTO": true}
	quitKW = map[string]bool{"Q": true, "QUIT": true}
	ifKW   = map[string]bool{"I": true, "IF": true}
)

type cmdInfo struct {
	node  parse.Node
	row   int
	isDot bool
}

type labelInfo struct {
	name string
	row  int
	col  int
}

// BuildCFGs builds one CFG per label in the routine.
func BuildCFGs(root parse.Node, src []byte) []CFG {
	labels, cmds := collect(root)
	if len(labels) == 0 {
		return nil
	}
	totalRows := strings.Count(string(src), "\n") + 1
	out := make([]CFG, 0, len(labels))
	for i, lab := range labels {
		end := totalRows
		if i+1 < len(labels) {
			end = labels[i+1].row
		}
		var body []cmdInfo
		for _, c := range cmds {
			if lab.row < c.row && c.row < end { // strict: label-line commands excluded (matches reference)
				body = append(body, c)
			}
		}
		out = append(out, buildOne(lab, body, src))
	}
	return out
}

// collect walks top-level `line` nodes, gathering labels (with positions) and
// commands (each tagged with its line's dot-block flag), in source order.
func collect(root parse.Node) ([]labelInfo, []cmdInfo) {
	var labels []labelInfo
	var cmds []cmdInfo
	for i := uint32(0); i < root.NamedChildCount(); i++ {
		line := root.NamedChild(i)
		if line.Type() != "line" {
			continue
		}
		isDot := false
		for j := uint32(0); j < line.ChildCount(); j++ {
			ch := line.Child(j)
			switch ch.Type() {
			case "label":
				sp := ch.StartPoint()
				labels = append(labels, labelInfo{name: string(ch.Text()), row: int(sp.Row), col: int(sp.Column)})
			case "dot_block_prefix":
				isDot = true
			}
		}
		for j := uint32(0); j < line.ChildCount(); j++ {
			cs := line.Child(j)
			if cs.Type() != "command_sequence" {
				continue
			}
			for k := uint32(0); k < cs.ChildCount(); k++ {
				if c := cs.Child(k); c.Type() == "command" {
					cmds = append(cmds, cmdInfo{node: c, row: int(c.StartPoint().Row), isDot: isDot})
				}
			}
		}
	}
	return labels, cmds
}

func buildOne(lab labelInfo, body []cmdInfo, src []byte) CFG {
	blocks := []Block{{ID: 0, Kind: "entry", Line: lab.row + 1}}
	for i, c := range body {
		blocks = append(blocks, Block{ID: i + 1, Kind: "command", Command: c.node, HasCmd: true, Line: c.row + 1})
	}
	exitID := len(blocks)
	blocks = append(blocks, Block{ID: exitID, Kind: "exit"})

	if len(body) > 0 {
		blocks[0].Succ, blocks[0].Edges = []int{1}, []string{"fall"}
	} else {
		blocks[0].Succ, blocks[0].Edges = []int{exitID}, []string{"fall"}
	}

	for i, c := range body {
		bid := i + 1
		nextID := bid + 1
		if nextID >= exitID {
			nextID = exitID
		}
		kw := commandKeyword(c.node, src)
		hasPC := hasChildType(c.node, "postconditional")
		switch {
		case exitKW[kw]:
			quitInDot := quitKW[kw] && c.isDot && !hasArgs(c.node)
			switch {
			case hasPC && quitInDot:
				blocks[bid].Succ, blocks[bid].Edges = []int{nextID, nextID}, []string{"fall", "skip"}
			case hasPC:
				blocks[bid].Succ, blocks[bid].Edges = []int{exitID, nextID}, []string{"branch", "skip"}
			case quitInDot:
				blocks[bid].Succ, blocks[bid].Edges = []int{nextID}, []string{"fall"}
			default:
				blocks[bid].Succ, blocks[bid].Edges = []int{exitID}, []string{"exit"}
			}
		case ifKW[kw] && !hasPC:
			skip := firstCmdAfterRow(body, c.row, exitID)
			blocks[bid].Succ, blocks[bid].Edges = []int{nextID, skip}, []string{"fall", "if-skip"}
		default:
			if hasPC {
				blocks[bid].Succ, blocks[bid].Edges = []int{nextID, nextID}, []string{"fall", "skip"}
			} else {
				blocks[bid].Succ, blocks[bid].Edges = []int{nextID}, []string{"fall"}
			}
		}
	}
	return CFG{LabelName: lab.name, LabelRow: lab.row, LabelCol: lab.col, Blocks: blocks}
}

// firstCmdAfterRow returns the block id of the first body command on a line
// after row, or exitID if none (an IF's false-skip target).
func firstCmdAfterRow(body []cmdInfo, row, exitID int) int {
	for i, c := range body {
		if c.row > row {
			return i + 1
		}
	}
	return exitID
}

func commandKeyword(cmd parse.Node, src []byte) string {
	for i := uint32(0); i < cmd.ChildCount(); i++ {
		if ch := cmd.Child(i); ch.Type() == "command_keyword" {
			s, e := ch.StartByte(), ch.EndByte()
			if int(e) <= len(src) && s <= e {
				return strings.ToUpper(string(src[s:e]))
			}
		}
	}
	return ""
}

func hasChildType(n parse.Node, typ string) bool {
	for i := uint32(0); i < n.ChildCount(); i++ {
		if n.Child(i).Type() == typ {
			return true
		}
	}
	return false
}

func hasArgs(cmd parse.Node) bool {
	for i := uint32(0); i < cmd.ChildCount(); i++ {
		al := cmd.Child(i)
		if al.Type() != "argument_list" {
			continue
		}
		for j := uint32(0); j < al.ChildCount(); j++ {
			if al.Child(j).Type() == "argument" {
				return true
			}
		}
	}
	return false
}
