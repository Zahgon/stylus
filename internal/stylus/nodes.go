/*!
 * Stylus - nodes
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go: the structural nodes (expressions, blocks,
 * selectors, at-rules, functions and colours).
 */

package stylus

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

/*
 * A JavaScript object, which Stylus relies on for key order: integer-like
 * keys first in ascending order, then the other keys in insertion order.
 */

type jsObj struct {
	order []string
	m     map[string]Node
}

func newJSObj() *jsObj { return &jsObj{m: map[string]Node{}} }

func (o *jsObj) set(k string, v Node) {
	if _, ok := o.m[k]; !ok {
		o.order = append(o.order, k)
	}
	o.m[k] = v
}

func (o *jsObj) get(k string) (Node, bool) {
	v, ok := o.m[k]
	return v, ok
}

func (o *jsObj) has(k string) bool { _, ok := o.m[k]; return ok }
func (o *jsObj) length() int       { return len(o.order) }

func isArrayIndex(k string) (uint32, bool) {
	if k == "" || len(k) > 10 {
		return 0, false
	}
	if k != "0" && k[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	if err != nil || n >= 4294967295 {
		return 0, false
	}
	return uint32(n), true
}

func (o *jsObj) keysInOrder() []string {
	var idx []uint32
	var rest []string
	for _, k := range o.order {
		if n, ok := isArrayIndex(k); ok {
			idx = append(idx, n)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i] < idx[j] })
	out := make([]string, 0, len(o.order))
	for _, n := range idx {
		out = append(out, strconv.FormatUint(uint64(n), 10))
	}
	return append(out, rest...)
}

/*
 * Expression
 */

// ExpressionNode is a space- or comma-separated list of values.
type ExpressionNode struct {
	BaseNode
	Nodes    []Node
	IsList   bool
	Preserve bool
}

func NewExpression(isList bool) *ExpressionNode {
	n := &ExpressionNode{IsList: isList}
	n.init(n)
	return n
}

func (n *ExpressionNode) NodeName() string { return "expression" }
func (n *ExpressionNode) IsEmpty() bool    { return len(n.Nodes) == 0 }
func (n *ExpressionNode) Push(node Node)   { n.Nodes = append(n.Nodes, node) }

func (n *ExpressionNode) First() Node {
	if len(n.Nodes) > 0 {
		return n.Nodes[0].First()
	}
	return NullNode_
}

func (n *ExpressionNode) Hash() any {
	parts := make([]string, len(n.Nodes))
	for i, node := range n.Nodes {
		parts[i] = joinValue(node.Hash())
	}
	return strings.Join(parts, "::")
}

// joinValue is what Array#join writes for a value: null and undefined become
// the empty string.
func joinValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case jsNull:
		return ""
	case string:
		return t
	case float64:
		return numToString(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

func (n *ExpressionNode) CloneNode(parent, node Node) Node {
	clone := NewExpression(n.IsList)
	clone.Preserve = n.Preserve
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Nodes = make([]Node, len(n.Nodes))
	for i, c := range n.Nodes {
		clone.Nodes[i] = c.CloneNode(parent, clone)
	}
	return clone
}

func (n *ExpressionNode) ToBoolean() *BooleanNode {
	if len(n.Nodes) > 1 {
		return TrueNode
	}
	return n.First().ToBoolean()
}

func (n *ExpressionNode) String() string {
	parts := make([]string, len(n.Nodes))
	for i, node := range n.Nodes {
		parts[i] = node.String()
	}
	sep := " "
	if n.IsList {
		sep = ", "
	}
	return "(" + strings.Join(parts, sep) + ")"
}

func (n *ExpressionNode) Operate(op string, right Node, val Node) Node {
	return expressionOperate(n, n, op, right, val)
}

// expressionOperate is Expression#operate; `self` is the node that inherits it
// (an Expression or an Arguments).
func expressionOperate(n *ExpressionNode, self Node, op string, right Node, val Node) Node {
	switch op {
	case "[]=":
		rng := nodesOf(unwrap(right))
		v := unwrap(val)
		for _, unit := range rng {
			length := len(n.Nodes)
			if u, ok := unit.(*UnitNode); ok {
				i := int(u.Val)
				if u.Val < 0 {
					i = length + int(u.Val)
				}
				if i < 0 {
					continue
				}
				for len(n.Nodes) <= i {
					n.Nodes = append(n.Nodes, NullNode_)
				}
				n.Nodes[i] = v
			} else if s := stringOf(unit); s != "" {
				if length > 0 {
					if obj, ok := n.Nodes[0].(*ObjectNode); ok {
						obj.SetValue(s, v.CloneNode(nil, nil))
					}
				}
			}
		}
		return v
	case "[]":
		expr := NewExpression(false)
		vals := nodesOf(unwrap(self))
		rng := nodesOf(unwrap(right))
		for _, unit := range rng {
			var node Node
			if u, ok := unit.(*UnitNode); ok {
				i := int(u.Val)
				if u.Val < 0 {
					i = len(vals) + int(u.Val)
				}
				if i >= 0 && i < len(vals) {
					node = vals[i]
				}
			} else if len(vals) > 0 && vals[0].NodeName() == "object" {
				node = vals[0].(*ObjectNode).Get(stringOf(unit))
			}
			if node != nil {
				expr.Push(node)
			}
		}
		if expr.IsEmpty() {
			return NullNode_
		}
		return unwrap(expr)
	case "||":
		if self.ToBoolean().Val {
			return self
		}
		return right
	case "in":
		return nodeOperate(self, op, right, val)
	case "!=":
		return self.Operate("==", right, val).(*BooleanNode).Negate()
	case "==":
		length := len(n.Nodes)
		r := right.ToExpression()
		if length != len(r.Nodes) {
			return FalseNode
		}
		for i := 0; i < length; i++ {
			a := n.Nodes[i]
			b := r.Nodes[i]
			res := a.Operate(op, b, nil)
			if bn, ok := res.(*BooleanNode); ok && bn.Val {
				continue
			}
			return FalseNode
		}
		return TrueNode
	}
	return n.First().Operate(op, right, val)
}

/*
 * Arguments
 */

// ArgumentsNode is an Expression that also carries named arguments.
type ArgumentsNode struct {
	ExpressionNode
	Map *jsObj
}

func NewArguments() *ArgumentsNode {
	n := &ArgumentsNode{Map: newJSObj()}
	n.init(n)
	return n
}

func (n *ArgumentsNode) NodeName() string { return "arguments" }

func ArgumentsFromExpression(expr *ExpressionNode) *ArgumentsNode {
	args := NewArguments()
	args.Lineno = expr.Lineno
	args.Column = expr.Column
	args.IsList = expr.IsList
	for _, node := range expr.Nodes {
		args.Push(node)
	}
	return args
}

func (n *ArgumentsNode) CloneNode(parent, node Node) Node {
	clone := NewArguments()
	clone.Preserve = n.Preserve
	clone.Nodes = make([]Node, len(n.Nodes))
	for i, c := range n.Nodes {
		clone.Nodes[i] = c.CloneNode(parent, clone)
	}
	for _, k := range n.Map.order {
		v, _ := n.Map.get(k)
		clone.Map.set(k, v.CloneNode(parent, clone))
	}
	clone.IsList = n.IsList
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *ArgumentsNode) Operate(op string, right Node, val Node) Node {
	return expressionOperate(&n.ExpressionNode, n, op, right, val)
}

/*
 * Block
 */

// BlockNode is the body of a rule, a function or the stylesheet.
type BlockNode struct {
	BaseNode
	Nodes  []Node
	Parent *BlockNode
	NodeOf Node
	Scope  bool
	// LacksRenderedSelectors is set by the compiler when a group's selectors
	// all turned out to be hidden.
	LacksRenderedSelectors bool
	Index                  int
	// isRootBlock marks the block that is really the Root node.
	isRootBlock bool
	// NodesGen counts the times Nodes was replaced by a fresh array. The
	// original wrote back through `block.nodes[block.index]`, so a write that
	// follows such a replacement lands in the array that was left behind.
	NodesGen int
}

func NewBlock(parent *BlockNode, node Node) *BlockNode {
	n := &BlockNode{Parent: parent, NodeOf: node, Scope: true}
	n.init(n)
	return n
}

func (n *BlockNode) NodeName() string { return "block" }
func (n *BlockNode) String() string   { return "[Block]" }
func (n *BlockNode) Push(node Node)   { n.Nodes = append(n.Nodes, node) }

func (n *BlockNode) HasProperties() bool {
	for _, node := range n.Nodes {
		if node.NodeName() == "property" {
			return true
		}
	}
	return false
}

func (n *BlockNode) HasMedia() bool {
	for _, node := range n.Nodes {
		if node.NodeName() == "media" {
			return true
		}
	}
	return false
}

func (n *BlockNode) IsEmpty() bool {
	if len(n.Nodes) == 0 {
		return true
	}
	for _, node := range n.Nodes {
		if node.NodeName() != "comment" {
			return false
		}
	}
	return true
}

func (n *BlockNode) CloneNode(parent, node Node) Node { return n.CloneBlock(parent, node) }

func (n *BlockNode) CloneBlock(parent, node Node) *BlockNode {
	p := n.Parent
	if pb, ok := parent.(*BlockNode); ok && parent != nil {
		p = pb
	}
	nodeOf := n.NodeOf
	if node != nil {
		nodeOf = node
	}
	clone := NewBlock(p, nodeOf)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Scope = n.Scope
	for _, c := range n.Nodes {
		clone.Push(c.CloneNode(clone, clone))
	}
	return clone
}

/*
 * Root
 */

// RootNode is the top of the parse tree. The original's Root also served as
// the global frame's block, so the port builds it on BlockNode.
type RootNode struct {
	BlockNode
}

func NewRoot() *RootNode {
	n := &RootNode{}
	n.Scope = true
	n.isRootBlock = true
	n.init(n)
	return n
}

func (n *RootNode) NodeName() string  { return "root" }
func (n *RootNode) String() string    { return "[Root]" }
func (n *RootNode) Unshift(node Node) { n.Nodes = append([]Node{node}, n.Nodes...) }

func (n *RootNode) CloneNode(parent, node Node) Node {
	clone := NewRoot()
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	for _, c := range n.Nodes {
		clone.Push(c.CloneNode(clone, clone))
	}
	return clone
}

/*
 * Group
 */

// GroupNode is a set of selectors sharing one block.
type GroupNode struct {
	BaseNode
	Nodes   []Node
	Extends []ExtendSel
}

// ExtendSel is one @extend target recorded on a group.
type ExtendSel struct {
	Selector string
	Optional bool
	Lineno   int
	Column   int
}

func NewGroup() *GroupNode {
	n := &GroupNode{}
	n.init(n)
	return n
}

func (n *GroupNode) NodeName() string { return "group" }
func (n *GroupNode) String() string   { return "[Group]" }
func (n *GroupNode) Push(sel Node)    { n.Nodes = append(n.Nodes, sel) }

func (n *GroupNode) GetBlock() *BlockNode {
	if len(n.Nodes) == 0 {
		return nil
	}
	return n.Nodes[0].base().Block
}

func (n *GroupNode) SetBlock(block *BlockNode) {
	for _, s := range n.Nodes {
		s.base().Block = block
	}
}

func (n *GroupNode) HasOnlyPlaceholders() bool {
	for _, s := range n.Nodes {
		if sel, ok := s.(*SelectorNode); !ok || !sel.IsPlaceholder() {
			return false
		}
	}
	return true
}

func (n *GroupNode) CloneNode(parent, node Node) Node {
	clone := NewGroup()
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	for _, c := range n.Nodes {
		clone.Push(c.CloneNode(parent, clone))
	}
	clone.Filename = n.Filename
	clone.SetBlock(n.GetBlock().CloneBlock(parent, clone))
	return clone
}

/*
 * Selector
 */

// SelectorNode is one selector of a group.
type SelectorNode struct {
	BaseNode
	Inherits bool
	Segments []Node
	Optional bool
	Val      string
}

func NewSelector(segs []Node) *SelectorNode {
	n := &SelectorNode{Inherits: true, Segments: segs}
	n.init(n)
	return n
}

func (n *SelectorNode) NodeName() string { return "selector" }

func (n *SelectorNode) String() string {
	var b strings.Builder
	for _, s := range n.Segments {
		b.WriteString(s.String())
	}
	if n.Optional {
		b.WriteString(" !optional")
	}
	return b.String()
}

func (n *SelectorNode) IsPlaceholder() bool {
	if n.Val == "" {
		return false
	}
	head := n.Val
	if len(head) > 2 {
		head = head[:2]
	}
	return strings.Contains(head, "$")
}

func (n *SelectorNode) CloneNode(parent, node Node) Node {
	clone := NewSelector(nil)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Inherits = n.Inherits
	clone.Val = n.Val
	clone.Segments = make([]Node, len(n.Segments))
	for i, s := range n.Segments {
		clone.Segments[i] = s.CloneNode(parent, clone)
	}
	clone.Optional = n.Optional
	return clone
}

/*
 * Property
 */

// PropertyNode is a CSS declaration.
type PropertyNode struct {
	BaseNode
	Segments []Node
	Expr     Node
	Name     string
	Literal  bool
}

func NewProperty(segs []Node, expr Node) *PropertyNode {
	n := &PropertyNode{Segments: segs, Expr: expr}
	n.init(n)
	return n
}

func (n *PropertyNode) NodeName() string { return "property" }

func (n *PropertyNode) String() string {
	var b strings.Builder
	for _, s := range n.Segments {
		b.WriteString(s.String())
	}
	e := "undefined"
	if n.Expr != nil {
		e = n.Expr.String()
	}
	return "property(" + b.String() + ", " + e + ")"
}

func (n *PropertyNode) CloneNode(parent, node Node) Node {
	clone := NewProperty(n.Segments, nil)
	clone.Name = n.Name
	clone.Literal = n.Literal
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Segments = make([]Node, len(n.Segments))
	for i, s := range n.Segments {
		clone.Segments[i] = s.CloneNode(parent, clone)
	}
	if n.Expr != nil {
		clone.Expr = n.Expr.CloneNode(parent, clone)
	}
	return clone
}

func (n *PropertyNode) Operate(op string, right Node, val Node) Node {
	return n.Expr.Operate(op, right, val)
}

/*
 * Object
 */

// ObjectNode is a hash literal.
type ObjectNode struct {
	BaseNode
	Vals *jsObj
	Keys *jsObj
}

func NewObject() *ObjectNode {
	n := &ObjectNode{Vals: newJSObj(), Keys: newJSObj()}
	n.init(n)
	return n
}

func (n *ObjectNode) NodeName() string { return "object" }

func (n *ObjectNode) SetValue(key string, val Node) *ObjectNode {
	n.Vals.set(key, val)
	return n
}

func (n *ObjectNode) SetKey(key string, val Node) *ObjectNode {
	n.Keys.set(key, val)
	return n
}

func (n *ObjectNode) Length() int { return n.Vals.length() }

func (n *ObjectNode) Get(key string) Node {
	if v, ok := n.Vals.get(key); ok && v != nil {
		return v
	}
	return NullNode_
}

func (n *ObjectNode) Has(key string) bool { return n.Vals.has(key) }

func (n *ObjectNode) ToBoolean() *BooleanNode { return NewBoolean(n.Length() != 0) }

func (n *ObjectNode) Operate(op string, right Node, val Node) Node {
	switch op {
	case ".", "[]":
		return n.Get(hashKey(right.Hash()))
	case "==":
		if right.NodeName() != "object" || n.Length() != right.(*ObjectNode).Length() {
			return FalseNode
		}
		other := right.(*ObjectNode)
		for _, key := range n.Vals.keysInOrder() {
			a, _ := n.Vals.get(key)
			b, ok := other.Vals.get(key)
			if !ok {
				return FalseNode
			}
			res := a.Operate(op, b, nil)
			if bn, ok := res.(*BooleanNode); ok && !bn.Val {
				return FalseNode
			}
		}
		return TrueNode
	case "!=":
		return n.Operate("==", right, nil).(*BooleanNode).Negate()
	}
	return nodeOperate(n, op, right, val)
}

func (n *ObjectNode) ToBlock() string {
	var b strings.Builder
	b.WriteString("{")
	for _, key := range n.Vals.keysInOrder() {
		val := n.Get(key)
		if val.First().NodeName() == "object" {
			b.WriteString(key + " " + val.First().(*ObjectNode).ToBlock())
		} else if key == "@charset" {
			b.WriteString(key + " " + val.First().String() + ";")
		} else {
			b.WriteString(key + ":" + objToString(val) + ";")
		}
	}
	b.WriteString("}")
	return b.String()
}

func objToString(node Node) string {
	if sub := nodesOf(node); sub != nil {
		parts := make([]string, len(sub))
		for i, c := range sub {
			parts[i] = objToString(c)
		}
		sep := " "
		if e, ok := node.(*ExpressionNode); ok && e.IsList {
			sep = ","
		}
		if a, ok := node.(*ArgumentsNode); ok && a.IsList {
			sep = ","
		}
		return strings.Join(parts, sep)
	}
	if lit, ok := node.(*LiteralNode); ok && lit.Val == "," {
		return "\\,"
	}
	return node.String()
}

func (n *ObjectNode) CloneNode(parent, node Node) Node {
	clone := NewObject()
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	for _, k := range n.Vals.order {
		v, _ := n.Vals.get(k)
		clone.Vals.set(k, v.CloneNode(parent, clone))
	}
	for _, k := range n.Keys.order {
		v, _ := n.Keys.get(k)
		clone.Keys.set(k, v.CloneNode(parent, clone))
	}
	return clone
}

func (n *ObjectNode) String() string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for _, k := range n.Vals.keysInOrder() {
		if !first {
			b.WriteString(",")
		}
		first = false
		v, _ := n.Vals.get(k)
		b.WriteString(jsonQuote(k) + ":" + jsonQuote(v.String()))
	}
	b.WriteString("}")
	return b.String()
}

// jsonQuote writes a string as JSON.stringify would.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '\b':
			b.WriteString("\\b")
		case '\f':
			b.WriteString("\\f")
		default:
			if r < 0x20 {
				b.WriteString("\\u" + strings.ToLower(pad4(int(r))))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pad4(n int) string {
	s := strconv.FormatInt(int64(n), 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

/*
 * Call, BinOp, UnaryOp, Ternary, If, Each, Member
 */

// CallNode is a function call or a mixin invocation.
type CallNode struct {
	BaseNode
	Name string
	Args *ArgumentsNode
}

func NewCall(name string, args *ArgumentsNode) *CallNode {
	n := &CallNode{Name: name, Args: args}
	n.init(n)
	return n
}

func (n *CallNode) NodeName() string { return "call" }

func (n *CallNode) String() string {
	parts := make([]string, len(n.Args.Nodes))
	for i, node := range n.Args.Nodes {
		s := node.String()
		if len(s) >= 2 {
			s = s[1 : len(s)-1]
		}
		parts[i] = s
	}
	return n.Name + "(" + strings.Join(parts, ", ") + ")"
}

func (n *CallNode) CloneNode(parent, node Node) Node {
	clone := NewCall(n.Name, nil)
	clone.Args = n.Args.CloneNode(parent, clone).(*ArgumentsNode)
	if n.Block != nil {
		clone.Block = n.Block.CloneBlock(parent, clone)
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// BinOpNode is a binary operation.
type BinOpNode struct {
	BaseNode
	Op    string
	Left  Node
	Right Node
	Val   Node
}

func NewBinOp(op string, left, right Node) *BinOpNode {
	n := &BinOpNode{Op: op, Left: left, Right: right}
	n.init(n)
	return n
}

func (n *BinOpNode) NodeName() string { return "binop" }

func (n *BinOpNode) String() string {
	r := "undefined"
	if n.Right != nil {
		r = n.Right.String()
	}
	return n.Left.String() + " " + n.Op + " " + r
}

func (n *BinOpNode) CloneNode(parent, node Node) Node {
	clone := NewBinOp(n.Op, nil, nil)
	clone.Left = n.Left.CloneNode(parent, clone)
	if n.Right != nil {
		clone.Right = n.Right.CloneNode(parent, clone)
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	if n.Val != nil {
		clone.Val = n.Val.CloneNode(parent, clone)
	}
	return clone
}

// UnaryOpNode is a prefix operation.
type UnaryOpNode struct {
	BaseNode
	Op   string
	Expr Node
}

func NewUnaryOp(op string, expr Node) *UnaryOpNode {
	n := &UnaryOpNode{Op: op, Expr: expr}
	n.init(n)
	return n
}

func (n *UnaryOpNode) NodeName() string { return "unaryop" }
func (n *UnaryOpNode) String() string   { return n.Op + " " + n.Expr.String() }

func (n *UnaryOpNode) CloneNode(parent, node Node) Node {
	clone := NewUnaryOp(n.Op, nil)
	clone.Expr = n.Expr.CloneNode(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// TernaryNode is `cond ? a : b`.
type TernaryNode struct {
	BaseNode
	Cond      Node
	TrueExpr  Node
	FalseExpr Node
}

func NewTernary(cond, t, f Node) *TernaryNode {
	n := &TernaryNode{Cond: cond, TrueExpr: t, FalseExpr: f}
	n.init(n)
	return n
}

func (n *TernaryNode) NodeName() string { return "ternary" }
func (n *TernaryNode) String() string   { return "[Ternary]" }

func (n *TernaryNode) CloneNode(parent, node Node) Node {
	clone := NewTernary(nil, nil, nil)
	clone.Cond = n.Cond.CloneNode(parent, clone)
	clone.TrueExpr = n.TrueExpr.CloneNode(parent, clone)
	clone.FalseExpr = n.FalseExpr.CloneNode(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// IfNode is `if`, `unless` and their `else` branches.
type IfNode struct {
	BaseNode
	Cond Node
	// Block is whatever the original assigned to `if.block`: a Block for a
	// normal `if`, or the single statement of a postfix `if`.
	Block   Node
	Elses   []Node
	Negate  bool
	Postfix bool
}

func NewIf(cond Node, negate bool) *IfNode {
	n := &IfNode{Cond: cond, Negate: negate}
	n.init(n)
	return n
}

func (n *IfNode) NodeName() string { return "if" }
func (n *IfNode) String() string   { return "[If]" }

func (n *IfNode) CloneNode(parent, node Node) Node {
	clone := NewIf(nil, false)
	clone.Cond = n.Cond.CloneNode(parent, clone)
	clone.Block = n.Block.CloneNode(parent, clone)
	clone.Elses = make([]Node, len(n.Elses))
	for i, e := range n.Elses {
		clone.Elses[i] = e.CloneNode(parent, clone)
	}
	clone.Negate = n.Negate
	clone.Postfix = n.Postfix
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// EachNode is a `for` loop.
type EachNode struct {
	BaseNode
	Val  string
	Key  string
	Expr Node
}

func NewEach(val, key string, expr Node, block *BlockNode) *EachNode {
	n := &EachNode{Val: val, Key: key, Expr: expr}
	n.init(n)
	n.Block = block
	return n
}

func (n *EachNode) NodeName() string { return "each" }
func (n *EachNode) String() string   { return "[Each]" }

func (n *EachNode) CloneNode(parent, node Node) Node {
	clone := NewEach(n.Val, n.Key, nil, nil)
	clone.Expr = n.Expr.CloneNode(parent, clone)
	clone.Block = n.Block.CloneBlock(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// MemberNode is `left.right`.
type MemberNode struct {
	BaseNode
	Left  Node
	Right Node
	Val   Node
}

func NewMember(left, right Node) *MemberNode {
	n := &MemberNode{Left: left, Right: right}
	n.init(n)
	return n
}

func (n *MemberNode) NodeName() string { return "member" }
func (n *MemberNode) String() string   { return n.Left.String() + "." + n.Right.String() }

func (n *MemberNode) CloneNode(parent, node Node) Node {
	clone := NewMember(nil, nil)
	clone.Left = n.Left.CloneNode(parent, clone)
	clone.Right = n.Right.CloneNode(parent, clone)
	if n.Val != nil {
		clone.Val = n.Val.CloneNode(parent, clone)
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

/*
 * Import, Extend, Function, Params
 */

// ImportNode is @import or @require.
type ImportNode struct {
	BaseNode
	Path Node
	Once bool
}

func NewImport(expr Node, once bool) *ImportNode {
	n := &ImportNode{Path: expr, Once: once}
	n.init(n)
	return n
}

func (n *ImportNode) NodeName() string { return "import" }
func (n *ImportNode) String() string   { return "@import" }

func (n *ImportNode) CloneNode(parent, node Node) Node {
	clone := NewImport(nil, n.Once)
	clone.Path = n.Path.CloneNode(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// ExtendNode is @extend.
type ExtendNode struct {
	BaseNode
	Selectors     []ExtendSel
	SelectorNodes []*SelectorNode
}

func NewExtend(selectors []ExtendSel) *ExtendNode {
	n := &ExtendNode{Selectors: selectors}
	n.init(n)
	return n
}

func (n *ExtendNode) NodeName() string { return "extend" }

func (n *ExtendNode) String() string {
	parts := make([]string, len(n.SelectorNodes))
	for i, s := range n.SelectorNodes {
		parts[i] = s.String()
	}
	return "@extend " + strings.Join(parts, ", ")
}

func (n *ExtendNode) CloneNode(parent, node Node) Node {
	clone := NewExtend(n.Selectors)
	clone.SelectorNodes = n.SelectorNodes
	return clone
}

// BuiltinFn is the Go signature of a built-in function.
type BuiltinFn func(ev *Evaluator, args []Node) Node

// FunctionNode is a user function, a mixin or a built-in.
type FunctionNode struct {
	BaseNode
	Name   string
	Params *ParamsNode
	Fn     BuiltinFn
	// FnParams names the built-in's declared parameters, as the original read
	// them off the JavaScript function.
	FnParams []string
	// Raw built-ins receive their arguments unevaluated-but-not-unwrapped.
	Raw bool
}

func NewFunction(name string, params *ParamsNode, body *BlockNode) *FunctionNode {
	n := &FunctionNode{Name: name, Params: params}
	n.init(n)
	n.Block = body
	return n
}

func NewBuiltin(name string, fn BuiltinFn, params []string, raw bool) *FunctionNode {
	n := &FunctionNode{Name: name, Fn: fn, FnParams: params, Raw: raw}
	n.init(n)
	return n
}

func (n *FunctionNode) NodeName() string { return "function" }
func (n *FunctionNode) Hash() any        { return "function " + n.Name }

func (n *FunctionNode) Arity() int {
	if n.Params == nil {
		return len(n.FnParams)
	}
	return len(n.Params.Nodes)
}

func (n *FunctionNode) String() string {
	if n.Fn != nil {
		return n.Name + "(" + strings.Join(n.FnParams, ", ") + ")"
	}
	parts := make([]string, len(n.Params.Nodes))
	for i, p := range n.Params.Nodes {
		parts[i] = p.String()
	}
	return n.Name + "(" + strings.Join(parts, ", ") + ")"
}

func (n *FunctionNode) CloneNode(parent, node Node) Node {
	var clone *FunctionNode
	if n.Fn != nil {
		clone = NewBuiltin(n.Name, n.Fn, n.FnParams, n.Raw)
	} else {
		clone = NewFunction(n.Name, nil, nil)
		clone.Params = n.Params.CloneNode(parent, clone).(*ParamsNode)
		clone.Block = n.Block.CloneBlock(parent, clone)
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// ParamsNode is a function's parameter list.
type ParamsNode struct {
	BaseNode
	Nodes []Node
}

func NewParams() *ParamsNode {
	n := &ParamsNode{}
	n.init(n)
	return n
}

func (n *ParamsNode) NodeName() string { return "params" }
func (n *ParamsNode) String() string   { return "[Params]" }
func (n *ParamsNode) Push(node Node)   { n.Nodes = append(n.Nodes, node) }

func (n *ParamsNode) CloneNode(parent, node Node) Node {
	clone := NewParams()
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	for _, c := range n.Nodes {
		clone.Push(c.CloneNode(parent, clone))
	}
	return clone
}

/*
 * At-rules
 */

// AtruleNode is a generic at-rule.
type AtruleNode struct {
	BaseNode
	Type     string
	Segments []Node
	// ValStr is the at-rule's interpolated value, which the evaluator sets.
	ValStr string
	// Group and Bubbled are set by the normaliser when properties bubble out.
	Group   *GroupNode
	Bubbled bool
}

func NewAtrule(typ string) *AtruleNode {
	n := &AtruleNode{Type: typ}
	n.init(n)
	return n
}

func (n *AtruleNode) NodeName() string { return "atrule" }
func (n *AtruleNode) String() string   { return "@" + n.Type }

func (n *AtruleNode) HasOnlyProperties() bool {
	if n.Block == nil {
		return false
	}
	for _, node := range n.Block.Nodes {
		switch node.NodeName() {
		case "property", "expression", "comment":
			continue
		default:
			return false
		}
	}
	return true
}

func (n *AtruleNode) HasOutput() bool {
	return n.Block != nil && blockHasOutput(n.Block)
}

func blockHasOutput(block *BlockNode) bool {
	onlyPlaceholders := true
	for _, node := range block.Nodes {
		g, ok := node.(*GroupNode)
		if !ok || !g.HasOnlyPlaceholders() {
			onlyPlaceholders = false
			break
		}
	}
	if onlyPlaceholders {
		return false
	}
	for _, node := range block.Nodes {
		switch node.NodeName() {
		case "property", "literal", "import":
			return true
		case "block":
			if blockHasOutput(node.(*BlockNode)) {
				return true
			}
		default:
			if b := blockOf(node); b != nil {
				if blockHasOutput(b) {
					return true
				}
			}
		}
	}
	return false
}

func (n *AtruleNode) CloneNode(parent, node Node) Node {
	clone := NewAtrule(n.Type)
	if n.Block != nil {
		clone.Block = n.Block.CloneBlock(parent, clone)
	}
	clone.Segments = make([]Node, len(n.Segments))
	for i, s := range n.Segments {
		clone.Segments[i] = s.CloneNode(parent, clone)
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// KeyframesNode is @keyframes.
type KeyframesNode struct {
	AtruleNode
	Prefix     string
	Frames     int
	Fabricated bool
}

func NewKeyframes(segs []Node, prefix string) *KeyframesNode {
	if prefix == "" {
		prefix = "official"
	}
	n := &KeyframesNode{Prefix: prefix}
	n.Type = "keyframes"
	n.Segments = segs
	n.init(n)
	return n
}

func (n *KeyframesNode) NodeName() string { return "keyframes" }

func (n *KeyframesNode) String() string {
	var b strings.Builder
	b.WriteString("@keyframes ")
	for _, s := range n.Segments {
		b.WriteString(s.String())
	}
	return b.String()
}

func (n *KeyframesNode) CloneNode(parent, node Node) Node {
	clone := NewKeyframes(nil, "")
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Segments = make([]Node, len(n.Segments))
	for i, s := range n.Segments {
		clone.Segments[i] = s.CloneNode(parent, clone)
	}
	clone.Prefix = n.Prefix
	clone.ValStr = n.ValStr
	clone.Block = n.Block.CloneBlock(parent, clone)
	return clone
}

// MediaNode is @media.
type MediaNode struct {
	AtruleNode
	Val Node
}

func NewMedia(val Node) *MediaNode {
	n := &MediaNode{Val: val}
	n.Type = "media"
	n.init(n)
	return n
}

func (n *MediaNode) NodeName() string { return "media" }
func (n *MediaNode) String() string   { return "@media " + n.Val.String() }

func (n *MediaNode) CloneNode(parent, node Node) Node {
	clone := NewMedia(nil)
	clone.Val = n.Val.CloneNode(parent, clone)
	clone.Block = n.Block.CloneBlock(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// SupportsNode is @supports.
type SupportsNode struct {
	AtruleNode
	Condition Node
}

func NewSupports(condition Node) *SupportsNode {
	n := &SupportsNode{Condition: condition}
	n.Type = "supports"
	n.init(n)
	return n
}

func (n *SupportsNode) NodeName() string { return "supports" }
func (n *SupportsNode) String() string   { return "@supports " + n.Condition.String() }

func (n *SupportsNode) CloneNode(parent, node Node) Node {
	clone := NewSupports(nil)
	clone.Condition = n.Condition.CloneNode(parent, clone)
	clone.Block = n.Block.CloneBlock(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// QueryListNode is the comma-separated list of a media rule.
type QueryListNode struct {
	BaseNode
	Nodes []Node
}

func NewQueryList() *QueryListNode {
	n := &QueryListNode{}
	n.init(n)
	return n
}

func (n *QueryListNode) NodeName() string { return "querylist" }
func (n *QueryListNode) Push(node Node)   { n.Nodes = append(n.Nodes, node) }

func (n *QueryListNode) String() string {
	parts := make([]string, len(n.Nodes))
	for i, q := range n.Nodes {
		parts[i] = q.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func (n *QueryListNode) CloneNode(parent, node Node) Node {
	clone := NewQueryList()
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	for _, q := range n.Nodes {
		clone.Push(q.CloneNode(parent, clone))
	}
	return clone
}

func (n *QueryListNode) Merge(other *QueryListNode) *QueryListNode {
	list := NewQueryList()
	for _, q := range n.Nodes {
		for _, o := range other.Nodes {
			if merged := q.(*QueryNode).Merge(o.(*QueryNode)); merged != nil {
				list.Push(merged)
			}
		}
	}
	return list
}

// QueryNode is one media query.
type QueryNode struct {
	BaseNode
	Nodes     []Node
	Type      any // string or Node
	Predicate any // string or Node
}

func NewQuery() *QueryNode {
	n := &QueryNode{Type: "", Predicate: ""}
	n.init(n)
	return n
}

func (n *QueryNode) NodeName() string  { return "query" }
func (n *QueryNode) Push(feature Node) { n.Nodes = append(n.Nodes, feature) }

func resolvedStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case Node:
		return stringOf(t)
	}
	return ""
}

func nonEmpty(v any) bool {
	switch t := v.(type) {
	case string:
		return t != ""
	case nil:
		return false
	}
	return true
}

func (n *QueryNode) ResolvedType() string {
	if nonEmpty(n.Type) {
		return resolvedStr(n.Type)
	}
	return ""
}

func (n *QueryNode) ResolvedPredicate() string {
	if nonEmpty(n.Predicate) {
		return resolvedStr(n.Predicate)
	}
	return ""
}

func (n *QueryNode) CloneNode(parent, node Node) Node {
	clone := NewQuery()
	clone.Predicate = n.Predicate
	clone.Type = n.Type
	for _, f := range n.Nodes {
		clone.Push(f.CloneNode(parent, clone))
	}
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *QueryNode) Merge(other *QueryNode) *QueryNode {
	query := NewQuery()
	p1 := n.ResolvedPredicate()
	p2 := other.ResolvedPredicate()
	t1 := n.ResolvedType()
	t2 := other.ResolvedType()
	var typ, pred string
	if t1 == "" {
		t1 = t2
	}
	if t2 == "" {
		t2 = t1
	}
	switch {
	case (p1 == "not") != (p2 == "not"):
		if t1 == t2 {
			return nil
		}
		if p1 == "not" {
			typ, pred = t2, p2
		} else {
			typ, pred = t1, p1
		}
	case p1 == "not" && p2 == "not":
		if t1 != t2 {
			return nil
		}
		typ, pred = t1, "not"
	case t1 != t2:
		return nil
	default:
		typ = t1
		pred = p1
		if pred == "" {
			pred = p2
		}
	}
	query.Predicate = pred
	query.Type = typ
	query.Nodes = append(append([]Node{}, n.Nodes...), other.Nodes...)
	return query
}

func (n *QueryNode) String() string {
	pred := ""
	if nonEmpty(n.Predicate) {
		pred = jsToStr(n.Predicate) + " "
	}
	typ := ""
	if nonEmpty(n.Type) {
		typ = jsToStr(n.Type)
	}
	str := pred + typ
	if len(n.Nodes) > 0 {
		if typ != "" {
			str += " and "
		}
		parts := make([]string, len(n.Nodes))
		for i, e := range n.Nodes {
			parts[i] = e.String()
		}
		str += strings.Join(parts, " and ")
	}
	return str
}

func jsToStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case Node:
		return t.String()
	}
	return ""
}

// FeatureNode is one `(name: value)` of a media query.
type FeatureNode struct {
	BaseNode
	Segments []Node
	Expr     Node
	Name     string
}

func NewFeature(segs []Node) *FeatureNode {
	n := &FeatureNode{Segments: segs}
	n.init(n)
	return n
}

func (n *FeatureNode) NodeName() string { return "feature" }

func (n *FeatureNode) String() string {
	var b strings.Builder
	for _, s := range n.Segments {
		b.WriteString(s.String())
	}
	if n.Expr != nil {
		return "(" + b.String() + ": " + n.Expr.String() + ")"
	}
	return b.String()
}

func (n *FeatureNode) CloneNode(parent, node Node) Node {
	clone := NewFeature(nil)
	clone.Segments = make([]Node, len(n.Segments))
	for i, s := range n.Segments {
		clone.Segments[i] = s.CloneNode(parent, clone)
	}
	if n.Expr != nil {
		clone.Expr = n.Expr.CloneNode(parent, clone)
	}
	clone.Name = n.Name
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// AtblockNode is @block.
type AtblockNode struct {
	BaseNode
}

func NewAtblock() *AtblockNode {
	n := &AtblockNode{}
	n.init(n)
	return n
}

func (n *AtblockNode) NodeName() string { return "atblock" }
func (n *AtblockNode) String() string   { return "@block" }

func (n *AtblockNode) CloneNode(parent, node Node) Node {
	clone := NewAtblock()
	clone.Block = n.Block.CloneBlock(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

/*
 * Colours
 */

// RGBANode is a colour in the red/green/blue/alpha space.
type RGBANode struct {
	BaseNode
	R, G, B, A float64
	Name       string
	Raw        string
}

func clampRGB(n float64) float64 {
	v := parseFloatJS(toFixed(n, 0))
	return math.Max(0, math.Min(v, 255))
}

func clampAlpha(n float64) float64 { return math.Max(0, math.Min(n, 1)) }

func NewRGBA(r, g, b, a float64) *RGBANode {
	n := &RGBANode{R: clampRGB(r), G: clampRGB(g), B: clampRGB(b), A: clampAlpha(a)}
	n.init(n)
	return n
}

func RGBAWithoutClamping(r, g, b, a float64) *RGBANode {
	rgba := NewRGBA(0, 0, 0, 0)
	rgba.R, rgba.G, rgba.B, rgba.A = r, g, b, a
	return rgba
}

func (n *RGBANode) NodeName() string        { return "rgba" }
func (n *RGBANode) Hash() any               { return n.String() }
func (n *RGBANode) ToBoolean() *BooleanNode { return TrueNode }
func (n *RGBANode) HSLA() *HSLANode         { return HSLAFromRGBA(n) }

func (n *RGBANode) Clone() *RGBANode {
	clone := NewRGBA(n.R, n.G, n.B, n.A)
	clone.Raw = n.Raw
	clone.Name = n.Name
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *RGBANode) CloneNode(parent, node Node) Node { return n.Clone() }

func (n *RGBANode) Add(r, g, b, a float64) *RGBANode {
	return NewRGBA(n.R+r, n.G+g, n.B+b, n.A+a)
}

func (n *RGBANode) Sub(r, g, b, a float64) *RGBANode {
	na := n.A
	if a != 1 {
		na = n.A - a
	}
	return NewRGBA(n.R-r, n.G-g, n.B-b, na)
}

func (n *RGBANode) Multiply(x float64) *RGBANode {
	return NewRGBA(n.R*x, n.G*x, n.B*x, n.A)
}

func (n *RGBANode) Divide(x float64) *RGBANode {
	return NewRGBA(n.R/x, n.G/x, n.B/x, n.A)
}

func (n *RGBANode) Operate(op string, right Node, val Node) Node {
	if op != "in" {
		right = right.First()
	}
	switch op {
	case "is a":
		if s, ok := right.(*StringNode); ok && s.Str == "color" {
			return TrueNode
		}
	case "+":
		switch r := right.(type) {
		case *UnitNode:
			v := r.Val
			switch r.Type {
			case "%":
				return bifAdjust([]Node{n, NewString("lightness", ""), r})
			case "deg":
				return n.HSLA().AdjustHue(v).RGBA()
			default:
				return n.Add(v, v, v, 0)
			}
		case *RGBANode:
			return n.Add(r.R, r.G, r.B, r.A)
		case *HSLANode:
			return n.HSLA().Add(r.H, r.S, r.L)
		}
	case "-":
		switch r := right.(type) {
		case *UnitNode:
			v := r.Val
			switch r.Type {
			case "%":
				return bifAdjust([]Node{n, NewString("lightness", ""), NewUnit(-v, "%")})
			case "deg":
				return n.HSLA().AdjustHue(-v).RGBA()
			default:
				return n.Sub(v, v, v, 0)
			}
		case *RGBANode:
			return n.Sub(r.R, r.G, r.B, r.A)
		case *HSLANode:
			return n.HSLA().Sub(r.H, r.S, r.L)
		}
	case "*":
		if r, ok := right.(*UnitNode); ok {
			return n.Multiply(r.Val)
		}
	case "/":
		if r, ok := right.(*UnitNode); ok {
			return n.Divide(r.Val)
		}
	}
	return nodeOperate(n, op, right, val)
}

func (n *RGBANode) String() string {
	pad := func(v float64) string {
		s := toStringRadix(v, 16)
		if v < 16 {
			return "0" + s
		}
		return s
	}
	if n.Name == "transparent" {
		return n.Name
	}
	if n.A == 1 {
		r, g, b := pad(n.R), pad(n.G), pad(n.B)
		if r[0] == r[1] && g[0] == g[1] && b[0] == b[1] {
			return "#" + string(r[0]) + string(g[0]) + string(b[0])
		}
		return "#" + r + g + b
	}
	return "rgba(" + numToString(n.R) + "," + numToString(n.G) + "," + numToString(n.B) + "," +
		numToString(toFixedNum(n.A, 3)) + ")"
}

func RGBAFromHSLA(hsla *HSLANode) *RGBANode {
	h := hsla.H / 360
	s := hsla.S / 100
	l := hsla.L / 100
	a := hsla.A

	var m2 float64
	if l <= .5 {
		m2 = l * (s + 1)
	} else {
		m2 = l + s - l*s
	}
	m1 := l*2 - m2

	hue := func(h float64) float64 {
		if h < 0 {
			h++
		}
		if h > 1 {
			h--
		}
		if h*6 < 1 {
			return m1 + (m2-m1)*h*6
		}
		if h*2 < 1 {
			return m2
		}
		if h*3 < 2 {
			return m1 + (m2-m1)*(2.0/3-h)*6
		}
		return m1
	}

	return NewRGBA(hue(h+1.0/3)*0xff, hue(h)*0xff, hue(h-1.0/3)*0xff, a)
}

// HSLANode is a colour in the hue/saturation/lightness space.
type HSLANode struct {
	BaseNode
	H, S, L, A float64
}

func clampDegrees(n float64) float64 {
	n = math.Mod(n, 360)
	if n >= 0 {
		return n
	}
	return 360 + n
}

func clampPercentage(n float64) float64 { return math.Max(0, math.Min(n, 100)) }

func NewHSLA(h, s, l, a float64) *HSLANode {
	n := &HSLANode{H: clampDegrees(h), S: clampPercentage(s), L: clampPercentage(l), A: clampAlpha(a)}
	n.init(n)
	return n
}

func (n *HSLANode) NodeName() string { return "hsla" }
func (n *HSLANode) RGBA() *RGBANode  { return RGBAFromHSLA(n) }
func (n *HSLANode) Hash() any        { return n.RGBA().String() }

func (n *HSLANode) String() string {
	return "hsla(" + numToString(n.H) + "," + toFixed(n.S, 0) + "%," + toFixed(n.L, 0) + "%," + numToString(n.A) + ")"
}

func (n *HSLANode) CloneNode(parent, node Node) Node {
	clone := NewHSLA(n.H, n.S, n.L, n.A)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *HSLANode) Add(h, s, l float64) *HSLANode {
	return NewHSLA(n.H+h, n.S+s, n.L+l, n.A)
}

func (n *HSLANode) Sub(h, s, l float64) *HSLANode { return n.Add(-h, -s, -l) }

func (n *HSLANode) AdjustLightness(percent float64) *HSLANode {
	n.L = clampPercentage(n.L + n.L*(percent/100))
	return n
}

func (n *HSLANode) AdjustHue(deg float64) *HSLANode {
	n.H = clampDegrees(n.H + deg)
	return n
}

func (n *HSLANode) Operate(op string, right Node, val Node) Node {
	switch op {
	case "==", "!=", "<=", ">=", "<", ">", "is a", "||", "&&":
		return n.RGBA().Operate(op, right, val)
	}
	res := n.RGBA().Operate(op, right, val)
	if r, ok := res.(*RGBANode); ok {
		return r.HSLA()
	}
	return res
}

func HSLAFromRGBA(rgba *RGBANode) *HSLANode {
	r := rgba.R / 255
	g := rgba.G / 255
	b := rgba.B / 255
	a := rgba.A

	min := math.Min(r, math.Min(g, b))
	max := math.Max(r, math.Max(g, b))
	l := (max + min) / 2
	d := max - min
	var h, s float64

	switch {
	case max == min:
		h = 0
	case max == r:
		h = 60 * (g - b) / d
	case max == g:
		h = 60*(b-r)/d + 120
	case max == b:
		h = 60*(r-g)/d + 240
	}

	if max == min {
		s = 0
	} else if l < .5 {
		s = d / (2 * l)
	} else {
		s = d / (2 - 2*l)
	}

	h = math.Mod(h, 360)
	s *= 100
	l *= 100

	return NewHSLA(h, s, l, a)
}

// NewExtendSelectors builds an @extend whose targets are still selector nodes;
// the evaluator interpolates them into ExtendSel entries.
func NewExtendSelectors(sels []*SelectorNode) *ExtendNode {
	n := &ExtendNode{SelectorNodes: sels}
	n.init(n)
	return n
}

// hasNodes reports whether a node carries a `nodes` array at all, which the
// original tested with `if (expr.nodes)`.
func hasNodes(n Node) bool {
	switch n.(type) {
	case *ExpressionNode, *ArgumentsNode, *BlockNode, *RootNode, *GroupNode,
		*ParamsNode, *QueryListNode, *QueryNode:
		return true
	}
	return false
}

// nameOf is the `.name` property the original read off a node.
func nameOf(n Node) string {
	switch v := n.(type) {
	case *IdentNode:
		return v.Name
	case *FunctionNode:
		return v.Name
	case *CallNode:
		return v.Name
	case *RGBANode:
		return v.Name
	case *FeatureNode:
		return v.Name
	case *PropertyNode:
		return v.Name
	}
	return ""
}

// blockNodeName is the nodeName of a block, which is "root" for the Root.
func blockNodeName(b *BlockNode) string {
	if b == nil {
		return ""
	}
	if b.isRootBlock {
		return "root"
	}
	return "block"
}

// blockOf is the `block` property of a node: Group exposes its first
// selector's block, every other node keeps its own.
func blockOf(n Node) *BlockNode {
	if n == nil {
		return nil
	}
	if g, ok := n.(*GroupNode); ok {
		return g.GetBlock()
	}
	if ifn, ok := n.(*IfNode); ok {
		if b, isBlock := ifn.Block.(*BlockNode); isBlock {
			return b
		}
		return nil
	}
	return n.base().Block
}
