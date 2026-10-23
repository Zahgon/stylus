/*!
 * Stylus - Node
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go: the node base class and the value nodes.
 */

package stylus

import (
	"math"
	"strconv"
	"strings"
)

// Current source position, assigned to every node as it is constructed. The
// original kept these on the nodes module; the parser sets them per token.
var (
	curLineno   = 0
	curColumn   = 0
	curFilename = ""
)

// Node is the interface every syntax node implements. The methods mirror the
// ones the original's Node prototype exposed.
type Node interface {
	base() *BaseNode
	NodeName() string
	String() string
	CloneNode(parent, node Node) Node
	First() Node
	Hash() any
	JSVal() any
	ToBoolean() *BooleanNode
	ToExpression() *ExpressionNode
	ShouldCoerce(op string) bool
	Operate(op string, right Node, val Node) Node
	Coerce(other Node) Node
}

// BaseNode holds what the original's Node constructor set, plus `self`, which
// stands in for JavaScript's dynamic dispatch from an inherited method.
type BaseNode struct {
	self     Node
	Lineno   int
	Column   int
	Filename string
	// block is the `block` property several node types carry.
	Block *BlockNode
}

func (b *BaseNode) base() *BaseNode { return b }

func (b *BaseNode) init(self Node) {
	b.self = self
	b.Lineno = orOne(curLineno)
	b.Column = orOne(curColumn)
	b.Filename = curFilename
}

func orOne(n int) int {
	if n == 0 {
		return 1
	}
	return n
}

func (b *BaseNode) First() Node                   { return b.self }
func (b *BaseNode) Hash() any                     { return nil }
func (b *BaseNode) JSVal() any                    { return nil }
func (b *BaseNode) CloneNode(parent, n Node) Node { return b.self }
func (b *BaseNode) ToBoolean() *BooleanNode       { return TrueNode }

func (b *BaseNode) ToExpression() *ExpressionNode {
	if b.self.NodeName() == "expression" {
		return b.self.(*ExpressionNode)
	}
	expr := NewExpression(false)
	expr.Push(b.self)
	return expr
}

func (b *BaseNode) ShouldCoerce(op string) bool {
	switch op {
	case "is a", "in", "||", "&&":
		return false
	}
	return true
}

func (b *BaseNode) Operate(op string, right Node, val Node) Node {
	return nodeOperate(b.self, op, right, val)
}

func (b *BaseNode) Coerce(other Node) Node {
	return nodeCoerce(b.self, other)
}

// nodeOperate is the original Node#operate, the fallback every node type ends
// up in through `super.operate`.
func nodeOperate(self Node, op string, right Node, val Node) Node {
	switch op {
	case "is a":
		if right.First().NodeName() == "string" {
			return NewBoolean(jsLooseEq(self.NodeName(), right.JSVal()))
		}
		throwf("\"is a\" expects a string, got %s", right.String())
	case "==":
		return NewBoolean(jsLooseEq(self.Hash(), right.Hash()))
	case "!=":
		return NewBoolean(!jsLooseEq(self.Hash(), right.Hash()))
	case ">=", "<=", ">", "<":
		return NewBoolean(jsRelational(self.Hash(), right.Hash(), op))
	case "||":
		if self.ToBoolean().Val {
			return self
		}
		return right
	case "in":
		unwrapped := unwrap(right)
		vals := nodesOf(unwrapped)
		if vals == nil {
			throwf("\"in\" given invalid right-hand operand, expecting an expression")
		}
		hash := self.Hash()
		if len(vals) == 1 && vals[0].NodeName() == "object" {
			return NewBoolean(vals[0].(*ObjectNode).Has(hashKey(hash)))
		}
		for _, v := range vals {
			if jsLooseEq(hash, v.Hash()) {
				return TrueNode
			}
		}
		return FalseNode
	case "&&":
		a := self.ToBoolean()
		bb := right.ToBoolean()
		if a.Val && bb.Val {
			return right
		}
		if !a.Val {
			return self
		}
		return right
	}
	if op == "[]" {
		throwf("cannot perform %s[%s]", self.String(), right.String())
	}
	throwf("cannot perform %s %s %s", self.String(), op, right.String())
	return nil
}

func nodeCoerce(self Node, other Node) Node {
	if other.NodeName() == self.NodeName() {
		return other
	}
	throwNamed("CoercionError", "cannot coerce %s to %s", other.String(), self.NodeName())
	return nil
}

// nodesOf returns the `nodes` array of the expression-like nodes, or nil.
func nodesOf(n Node) []Node {
	switch v := n.(type) {
	case *ExpressionNode:
		return v.Nodes
	case *ArgumentsNode:
		return v.Nodes
	case *BlockNode:
		return v.Nodes
	case *RootNode:
		return v.Nodes
	case *GroupNode:
		return v.Nodes
	case *ParamsNode:
		return v.Nodes
	case *QueryListNode:
		return v.Nodes
	case *QueryNode:
		return v.Nodes
	case *AtblockNode:
		if v.Block != nil {
			return v.Block.Nodes
		}
		return nil
	}
	return nil
}

func hashKey(h any) string {
	switch v := h.(type) {
	case string:
		return v
	case float64:
		return numToString(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case jsNull:
		return "null"
	case nil:
		return "undefined"
	}
	return ""
}

// jsNull is the hash of the null node; a Go nil stands for JavaScript's
// undefined.
type jsNull struct{}

// jsLooseEq is JavaScript's == over the value kinds a node hash can hold.
func jsLooseEq(a, b any) bool {
	an, aIsNum := a.(float64)
	bn, bIsNum := b.(float64)
	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	_, aNull := a.(jsNull)
	_, bNull := b.(jsNull)
	aNullish := aNull || a == nil
	bNullish := bNull || b == nil
	if aNullish || bNullish {
		return aNullish && bNullish
	}
	if aIsStr && bIsStr {
		return as == bs
	}
	if aIsNum && bIsNum {
		return an == bn
	}
	ab, aIsBool := a.(bool)
	bb, bIsBool := b.(bool)
	if aIsBool && bIsBool {
		return ab == bb
	}
	// Mixed kinds compare as numbers, as JavaScript does.
	x, y := toNumber(a), toNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false
	}
	return x == y
}

// jsRelational is JavaScript's <, >, <= and >=: strings compare as strings,
// everything else as numbers.
func jsRelational(a, b any, op string) bool {
	as, aIsStr := a.(string)
	bs, bIsStr := b.(string)
	if aIsStr && bIsStr {
		switch op {
		case "<":
			return as < bs
		case ">":
			return as > bs
		case "<=":
			return as <= bs
		}
		return as >= bs
	}
	x, y := toNumber(a), toNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false
	}
	switch op {
	case "<":
		return x < y
	case ">":
		return x > y
	case "<=":
		return x <= y
	}
	return x >= y
}

func toNumber(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return n
	case bool:
		if t {
			return 1
		}
		return 0
	case jsNull:
		return 0
	case nil:
		return math.NaN()
	}
	return math.NaN()
}

// truthy is JavaScript's ToBoolean for the values passed to new Boolean(...).
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case int:
		return t != 0
	case string:
		return t != ""
	case nil:
		return false
	}
	return true
}

/*
 * Null
 */

// NullNode is the `null` value.
type NullNode struct{ BaseNode }

func NewNull() *NullNode {
	n := &NullNode{}
	n.init(n)
	return n
}

func (n *NullNode) NodeName() string        { return "null" }
func (n *NullNode) String() string          { return "null" }
func (n *NullNode) ToBoolean() *BooleanNode { return FalseNode }
func (n *NullNode) Hash() any               { return jsNull{} }

/*
 * Boolean
 */

// BooleanNode is `true` or `false`.
type BooleanNode struct {
	BaseNode
	Val bool
}

func NewBoolean(val bool) *BooleanNode {
	n := &BooleanNode{Val: val}
	n.init(n)
	return n
}

func (n *BooleanNode) NodeName() string        { return "boolean" }
func (n *BooleanNode) ToBoolean() *BooleanNode { return n }
func (n *BooleanNode) Hash() any               { return n.Val }
func (n *BooleanNode) JSVal() any              { return n.Val }
func (n *BooleanNode) Negate() *BooleanNode    { return NewBoolean(!n.Val) }

func (n *BooleanNode) String() string {
	if n.Val {
		return "true"
	}
	return "false"
}

/*
 * Ident
 */

// IdentNode is a bare word: a variable name, a property name or a keyword.
type IdentNode struct {
	BaseNode
	Name     string
	Str      string
	Val      Node
	Mixin    bool
	Property bool
	Rest     bool
}

func NewIdent(name string, val Node, mixin bool) *IdentNode {
	if val == nil {
		val = NullNode_
	}
	n := &IdentNode{Name: name, Str: name, Val: val, Mixin: mixin}
	n.init(n)
	return n
}

func (n *IdentNode) NodeName() string { return "ident" }
func (n *IdentNode) String() string   { return n.Name }
func (n *IdentNode) Hash() any        { return n.Name }
func (n *IdentNode) JSVal() any       { return n.Val }

func (n *IdentNode) CloneNode(parent, node Node) Node {
	clone := NewIdent(n.Name, nil, false)
	clone.Val = n.Val.CloneNode(parent, clone)
	clone.Mixin = n.Mixin
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	clone.Property = n.Property
	clone.Rest = n.Rest
	return clone
}

func (n *IdentNode) Coerce(other Node) Node {
	switch other.NodeName() {
	case "ident", "string", "literal":
		return NewIdent(stringOf(other), nil, false)
	case "unit":
		return NewIdent(other.String(), nil, false)
	}
	return nodeCoerce(n, other)
}

func (n *IdentNode) Operate(op string, right Node, val Node) Node {
	v := right.First()
	switch op {
	case "-":
		if v.NodeName() == "unit" {
			expr := NewExpression(false)
			u := v.(*UnitNode).Clone()
			u.Val = -u.Val
			expr.Push(n)
			expr.Push(u)
			return expr
		}
		return NewIdent(n.Str+stringOf(n.Coerce(v)), nil, false)
	case "+":
		return NewIdent(n.Str+stringOf(n.Coerce(v)), nil, false)
	}
	return nodeOperate(n, op, right, val)
}

// stringOf returns the `string` property the string-like nodes carry.
func stringOf(n Node) string {
	switch v := n.(type) {
	case *IdentNode:
		return v.Str
	case *StringNode:
		return v.Str
	case *LiteralNode:
		return v.Str
	}
	return ""
}

/*
 * Literal
 */

// LiteralNode is raw text that is written out unchanged.
type LiteralNode struct {
	BaseNode
	Val      string
	Str      string
	Prefixed bool
	CSS      bool
}

func NewLiteral(str string) *LiteralNode {
	n := &LiteralNode{Val: str, Str: str}
	n.init(n)
	return n
}

func (n *LiteralNode) NodeName() string { return "literal" }
func (n *LiteralNode) String() string   { return n.Val }
func (n *LiteralNode) Hash() any        { return n.Val }
func (n *LiteralNode) JSVal() any       { return n.Val }

func (n *LiteralNode) Coerce(other Node) Node {
	switch other.NodeName() {
	case "ident", "string", "literal":
		return NewLiteral(stringOf(other))
	}
	return nodeCoerce(n, other)
}

func (n *LiteralNode) Operate(op string, right Node, val Node) Node {
	v := right.First()
	if op == "+" {
		return NewLiteral(n.Str + stringOf(n.Coerce(v)))
	}
	return nodeOperate(n, op, right, val)
}

/*
 * String
 */

// StringNode is a quoted string.
type StringNode struct {
	BaseNode
	Val      string
	Str      string
	Quote    string
	Prefixed bool
}

func NewString(val string, quote string) *StringNode {
	if quote == "" {
		quote = "'"
	}
	n := &StringNode{Val: val, Str: val, Quote: quote}
	n.init(n)
	return n
}

func (n *StringNode) NodeName() string { return "string" }
func (n *StringNode) String() string   { return n.Quote + n.Val + n.Quote }
func (n *StringNode) Hash() any        { return n.Val }
func (n *StringNode) JSVal() any       { return n.Val }

func (n *StringNode) CloneNode(parent, node Node) Node {
	clone := NewString(n.Val, n.Quote)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *StringNode) ToBoolean() *BooleanNode { return NewBoolean(len(n.Val) != 0) }

func (n *StringNode) Coerce(other Node) Node {
	switch other.NodeName() {
	case "string":
		return other
	case "expression":
		parts := []string{}
		for _, node := range other.(*ExpressionNode).Nodes {
			parts = append(parts, n.Coerce(node).(*StringNode).Val)
		}
		return NewString(strings.Join(parts, " "), "")
	}
	return NewString(other.String(), "")
}

func (n *StringNode) Operate(op string, right Node, val Node) Node {
	switch op {
	case "%":
		expr := NewExpression(false)
		expr.Push(n)
		var args []Node
		if right.NodeName() == "expression" {
			args = nodesOf(unwrap(right))
		} else {
			args = []Node{right}
		}
		return bifSprintf(append([]Node{expr}, args...))
	case "+":
		expr := NewExpression(false)
		expr.Push(NewString(n.Val+n.Coerce(right).(*StringNode).Val, ""))
		return expr
	}
	return nodeOperate(n, op, right, val)
}

/*
 * Unit
 */

type unitFactor struct {
	val   float64
	label string
}

var factorTable = map[string]unitFactor{
	"mm":  {1, "mm"},
	"cm":  {10, "mm"},
	"in":  {25.4, "mm"},
	"pt":  {25.4 / 72, "mm"},
	"ms":  {1, "ms"},
	"s":   {1000, "ms"},
	"Hz":  {1, "Hz"},
	"kHz": {1000, "Hz"},
}

// UnitNode is a number with an optional unit such as px, % or deg.
type UnitNode struct {
	BaseNode
	Val  float64
	Type string
	Raw  string
}

func NewUnit(val float64, typ string) *UnitNode {
	n := &UnitNode{Val: val, Type: typ}
	n.init(n)
	return n
}

func (n *UnitNode) NodeName() string { return "unit" }
func (n *UnitNode) Hash() any        { return n.Val }
func (n *UnitNode) JSVal() any       { return n.Val }
func (n *UnitNode) String() string   { return numToString(n.Val) + n.Type }

func (n *UnitNode) ToBoolean() *BooleanNode {
	if n.Type != "" {
		return NewBoolean(true)
	}
	return NewBoolean(truthy(n.Val))
}

func (n *UnitNode) Clone() *UnitNode {
	clone := NewUnit(n.Val, n.Type)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

func (n *UnitNode) CloneNode(parent, node Node) Node { return n.Clone() }

func (n *UnitNode) Operate(op string, right Node, val Node) Node {
	typ := n.Type
	if typ == "" {
		if u, ok := right.First().(*UnitNode); ok {
			typ = u.Type
		}
	}

	// Swap color.
	if right.NodeName() == "rgba" || right.NodeName() == "hsla" {
		return right.Operate(op, n, nil)
	}

	if n.ShouldCoerce(op) {
		r := right.First()
		var coerced Node
		if ru2, ok := r.(*UnitNode); ok && n.Type != "%" && (op == "-" || op == "+") && ru2.Type == "%" {
			coerced = NewUnit(n.Val*(ru2.Val/100), "%")
		} else {
			coerced = n.Coerce(r)
		}
		if ru, ok := coerced.(*UnitNode); ok {
			switch op {
			case "-":
				return NewUnit(n.Val-ru.Val, typ)
			case "+":
				if typ == "" && ru.Type == "%" {
					typ = "%"
				}
				return NewUnit(n.Val+ru.Val, typ)
			case "/":
				return NewUnit(n.Val/ru.Val, typ)
			case "*":
				return NewUnit(n.Val*ru.Val, typ)
			case "%":
				return NewUnit(math.Mod(n.Val, ru.Val), typ)
			case "**":
				return NewUnit(jsPow(n.Val, ru.Val), typ)
			case "..", "...":
				start, end := n.Val, ru.Val
				expr := NewExpression(false)
				inclusive := op == ".."
				if start < end {
					for {
						expr.Push(NewUnit(start, ""))
						start++
						if inclusive {
							if !(start <= end) {
								break
							}
						} else if !(start < end) {
							break
						}
					}
				} else {
					for {
						expr.Push(NewUnit(start, ""))
						start--
						if inclusive {
							if !(start >= end) {
								break
							}
						} else if !(start > end) {
							break
						}
					}
				}
				return expr
			}
		}
		return nodeOperate(n, op, coerced, val)
	}

	return nodeOperate(n, op, right, val)
}

func (n *UnitNode) Coerce(other Node) Node {
	switch other.NodeName() {
	case "unit":
		b := other.(*UnitNode)
		fa, okA := factorTable[n.Type]
		fb, okB := factorTable[b.Type]
		if okA && okB && fa.label == fb.label {
			return NewUnit(b.Val*(fb.val/fa.val), n.Type)
		}
		return NewUnit(b.Val, n.Type)
	case "string":
		s := other.(*StringNode)
		// keyframes interpolation
		if s.Val == "%" {
			return NewUnit(0, "%")
		}
		v := parseFloatJS(s.Val)
		if math.IsNaN(v) {
			nodeCoerce(n, other)
		}
		return NewUnit(v, "")
	}
	return nodeCoerce(n, other)
}

// jsPow is Math.pow, which differs from Go's math.Pow for a few argument pairs.
func jsPow(x, y float64) float64 {
	if math.IsNaN(y) {
		return math.NaN()
	}
	if y == 0 {
		return 1
	}
	if (x == 1 || x == -1) && math.IsInf(y, 0) {
		return math.NaN()
	}
	return math.Pow(x, y)
}

/*
 * Comment
 */

// CommentNode is a /* ... */ comment.
type CommentNode struct {
	BaseNode
	Str      string
	Suppress bool
	Inline   bool
}

func NewComment(str string, suppress, inline bool) *CommentNode {
	n := &CommentNode{Str: str, Suppress: suppress, Inline: inline}
	n.init(n)
	return n
}

func (n *CommentNode) NodeName() string { return "comment" }
func (n *CommentNode) String() string   { return n.Str }

/*
 * Charset
 */

// CharsetNode is an @charset rule.
type CharsetNode struct {
	BaseNode
	Val string
}

func NewCharset(val string) *CharsetNode {
	n := &CharsetNode{Val: val}
	n.init(n)
	return n
}

func (n *CharsetNode) NodeName() string { return "charset" }
func (n *CharsetNode) String() string   { return "@charset " + n.Val }
func (n *CharsetNode) Hash() any        { return n.Val }
func (n *CharsetNode) JSVal() any       { return n.Val }

/*
 * Namespace
 */

// NamespaceNode is an @namespace rule.
type NamespaceNode struct {
	BaseNode
	Val    Node
	Prefix string
}

func NewNamespace(val Node, prefix string) *NamespaceNode {
	n := &NamespaceNode{Val: val, Prefix: prefix}
	n.init(n)
	return n
}

func (n *NamespaceNode) NodeName() string { return "namespace" }

func (n *NamespaceNode) String() string {
	p := ""
	if n.Prefix != "" {
		p = n.Prefix + " "
	}
	return "@namespace " + p + n.Val.String()
}

/*
 * Return
 */

// ReturnNode is a `return` statement.
type ReturnNode struct {
	BaseNode
	Expr Node
}

func NewReturn(expr Node) *ReturnNode {
	if expr == nil {
		expr = NullNode_
	}
	n := &ReturnNode{Expr: expr}
	n.init(n)
	return n
}

func (n *ReturnNode) NodeName() string { return "return" }
func (n *ReturnNode) String() string   { return "return" }

func (n *ReturnNode) CloneNode(parent, node Node) Node {
	clone := NewReturn(nil)
	clone.Expr = n.Expr.CloneNode(parent, clone)
	clone.Lineno = n.Lineno
	clone.Column = n.Column
	clone.Filename = n.Filename
	return clone
}

// Singletons, as the original's nodes module kept them.
var (
	TrueNode  *BooleanNode
	FalseNode *BooleanNode
	NullNode_ *NullNode
)

func init() {
	TrueNode = NewBoolean(true)
	FalseNode = NewBoolean(false)
	NullNode_ = NewNull()
}

var _ = strconv.Itoa
