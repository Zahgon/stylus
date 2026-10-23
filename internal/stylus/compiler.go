/*!
 * Stylus - Compiler
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import (
	"math"
	"path/filepath"
	"strings"
)

// Compiler renders the normalised tree as CSS.
type Compiler struct {
	root        Node
	Compress    bool
	Firebug     bool
	Linenos     bool
	spaces      int
	indents     int
	stack       [][]*SelectorNode
	buf         string
	keyframe    bool
	isURL       bool
	isCondition bool
	last        bool
}

// NewCompiler builds a compiler for `root`.
func NewCompiler(root Node, opts *Options) *Compiler {
	spaces := 2
	if opts != nil && opts.IndentSpaces > 0 {
		spaces = opts.IndentSpaces
	}
	c := &Compiler{root: root, spaces: spaces, indents: 1}
	if opts != nil {
		c.Compress = opts.Compress
		c.Firebug = opts.Firebug
		c.Linenos = opts.Linenos
	}
	return c
}

// Compile renders the tree.
func (c *Compiler) Compile() string {
	return c.visit(c.root)
}

func (c *Compiler) out(str string) string { return str }

func (c *Compiler) indent() string {
	if c.Compress {
		return ""
	}
	return strings.Repeat(strings.Repeat(" ", c.spaces), c.indents-1)
}

func (c *Compiler) needBrackets(node Node) bool {
	if c.indents == 1 {
		return true
	}
	if node == nil || node.NodeName() != "atrule" {
		return true
	}
	if a, ok := node.(*AtruleNode); ok {
		return a.HasOnlyProperties()
	}
	return true
}

func (c *Compiler) visit(node Node) string {
	switch n := node.(type) {
	case *RootNode:
		return c.visitRoot(n)
	case *BlockNode:
		c.visitBlock(n)
		return ""
	case *KeyframesNode:
		c.visitKeyframes(n)
		return ""
	case *MediaNode:
		c.visitMedia(n)
		return ""
	case *QueryListNode:
		c.visitQueryList(n)
		return ""
	case *QueryNode:
		c.visitQuery(n)
		return ""
	case *FeatureNode:
		return c.visitFeature(n)
	case *ImportNode:
		c.buf += c.out("@import " + c.visit(n.Path) + ";\n")
		return ""
	case *AtruleNode:
		c.visitAtrule(n)
		return ""
	case *SupportsNode:
		c.visitSupports(n)
		return ""
	case *CommentNode:
		if c.Compress && n.Suppress {
			return ""
		}
		return n.Str
	case *FunctionNode:
		return n.Name
	case *CharsetNode:
		return "@charset " + n.Val + ";"
	case *NamespaceNode:
		prefix := ""
		if n.Prefix != "" {
			prefix = n.Prefix + " "
		}
		return "@namespace " + prefix + c.visit(n.Val) + ";"
	case *LiteralNode:
		val := n.Val
		if n.CSS {
			val = stripTwoSpaceIndent(val)
		}
		return val
	case *BooleanNode:
		return n.String()
	case *RGBANode:
		return n.String()
	case *HSLANode:
		return n.RGBA().String()
	case *UnitNode:
		return c.visitUnit(n)
	case *GroupNode:
		c.visitGroup(n)
		return ""
	case *IdentNode:
		return n.Name
	case *StringNode:
		if c.isURL {
			return n.Val
		}
		return n.String()
	case *NullNode:
		return ""
	case *CallNode:
		return c.visitCall(n)
	case *ExpressionNode:
		return c.visitExpression(n.Nodes, n.IsList)
	case *ArgumentsNode:
		return c.visitExpression(n.Nodes, n.IsList)
	case *PropertyNode:
		return c.visitProperty(n)
	}
	// The original's Visitor returned the node itself for anything it had no
	// method for; the callers then stringified it.
	return node.String()
}

// stripTwoSpaceIndent is replace(/^  /gm, ”).
func stripTwoSpaceIndent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "  ")
	}
	return strings.Join(lines, "\n")
}

func (c *Compiler) visitRoot(block *RootNode) string {
	c.buf = ""
	for _, node := range block.Nodes {
		if c.Linenos || c.Firebug {
			c.debugInfo(node)
		}
		ret := c.visit(node)
		if ret != "" {
			c.buf += c.out(ret + "\n")
		}
	}
	return c.buf
}

func (c *Compiler) visitBlock(block *BlockNode) {
	separator := "\n"
	if c.Compress {
		separator = ""
	}
	lastPropertyIndex := -1

	if block.HasProperties() && !block.LacksRenderedSelectors {
		needBrackets := c.needBrackets(block.NodeOf)
		if c.Compress {
			for i := len(block.Nodes) - 1; i >= 0; i-- {
				if block.Nodes[i].NodeName() == "property" {
					lastPropertyIndex = i
					break
				}
			}
		}
		if needBrackets {
			if c.Compress {
				c.buf += c.out("{")
			} else {
				c.buf += c.out(" {\n")
			}
			c.indents++
		}
		for i, node := range block.Nodes {
			c.last = lastPropertyIndex == i
			switch node.NodeName() {
			case "null", "expression", "function", "group", "block", "unit",
				"media", "keyframes", "atrule", "supports":
				continue
			case "comment":
				cm := node.(*CommentNode)
				if !c.Compress && cm.Inline {
					if len(c.buf) > 0 {
						c.buf = c.buf[:len(c.buf)-1]
					}
					c.buf += c.out(" " + c.visit(node) + "\n")
					continue
				}
				c.buf += c.out(c.visit(node) + separator)
			case "property":
				ret := c.visit(node) + separator
				c.buf += ret
			default:
				// The original read `this.buf` before visiting, so an import,
				// which appends to the buffer itself, loses that append here
				// and prints "undefined" instead.
				old := c.buf
				text := c.visit(node)
				if node.NodeName() == "import" {
					text = "undefined"
				}
				c.buf = old + c.out(text+separator)
			}
		}
		if needBrackets {
			c.indents--
			c.buf += c.out(c.indent() + "}" + separator)
		}
	}

	// Nesting
	for _, node := range block.Nodes {
		switch node.NodeName() {
		case "group", "block", "keyframes":
			if c.Linenos || c.Firebug {
				c.debugInfo(node)
			}
			c.visit(node)
		case "media", "import", "atrule", "supports":
			c.visit(node)
		case "comment":
			if !node.(*CommentNode).Suppress {
				c.buf += c.out(c.indent() + c.visit(node) + "\n")
			}
		case "charset", "literal", "namespace":
			c.buf += c.out(c.visit(node) + "\n")
		}
	}
}

func (c *Compiler) visitKeyframes(node *KeyframesNode) {
	if node.Frames == 0 {
		return
	}
	prefix := ""
	if node.Prefix != "official" {
		prefix = "-" + node.Prefix + "-"
	}
	open := " {\n"
	if c.Compress {
		open = "{"
	}
	c.buf += c.out("@" + prefix + "keyframes " + node.ValStr + open)
	c.keyframe = true
	c.indents++
	c.visit(node.Block)
	c.indents--
	c.keyframe = false
	if c.Compress {
		c.buf += c.out("}")
	} else {
		c.buf += c.out("}\n")
	}
}

func (c *Compiler) visitMedia(media *MediaNode) {
	val, _ := media.Val.(*QueryListNode)
	if !media.HasOutput() || val == nil || len(val.Nodes) == 0 {
		return
	}
	c.buf += c.out("@media ")
	c.visit(val)
	if c.Compress {
		c.buf += c.out("{")
	} else {
		c.buf += c.out(" {\n")
	}
	c.indents++
	c.visit(media.Block)
	c.indents--
	if c.Compress {
		c.buf += c.out("}")
	} else {
		c.buf += c.out("}\n")
	}
}

func (c *Compiler) visitQueryList(queries *QueryListNode) {
	for i, q := range queries.Nodes {
		c.visit(q)
		if i != len(queries.Nodes)-1 {
			if c.Compress {
				c.buf += c.out(",")
			} else {
				c.buf += c.out(", ")
			}
		}
	}
}

func (c *Compiler) visitQuery(node *QueryNode) {
	length := len(node.Nodes)
	if nonEmpty(node.Predicate) {
		c.buf += c.out(jsToStr(node.Predicate) + " ")
	}
	if nonEmpty(node.Type) {
		and := ""
		if length > 0 {
			and = " and "
		}
		c.buf += c.out(jsToStr(node.Type) + and)
	}
	for i, f := range node.Nodes {
		c.buf += c.out(c.visit(f))
		if i != length-1 {
			c.buf += c.out(" and ")
		}
	}
}

func (c *Compiler) visitFeature(node *FeatureNode) string {
	if node.Expr == nil {
		return node.Name
	}
	if e, ok := node.Expr.(*ExpressionNode); ok && e.IsEmpty() {
		return "(" + node.Name + ")"
	}
	space := " "
	if c.Compress {
		space = ""
	}
	return "(" + node.Name + ":" + space + c.visit(node.Expr) + ")"
}

func (c *Compiler) visitAtrule(atrule *AtruleNode) {
	newline := "\n"
	if c.Compress {
		newline = ""
	}
	c.buf += c.out(c.indent() + "@" + atrule.Type)
	if atrule.ValStr != "" {
		c.buf += c.out(" " + strings.TrimSpace(atrule.ValStr))
	}
	if atrule.Block != nil {
		if atrule.Block.IsEmpty() {
			space := " "
			if c.Compress {
				space = ""
			}
			c.buf += c.out(space + "{}" + newline)
		} else if atrule.HasOnlyProperties() {
			c.visit(atrule.Block)
		} else {
			if c.Compress {
				c.buf += c.out("{")
			} else {
				c.buf += c.out(" {\n")
			}
			c.indents++
			c.visit(atrule.Block)
			c.indents--
			c.buf += c.out(c.indent() + "}" + newline)
		}
	} else {
		c.buf += c.out(";" + newline)
	}
}

func (c *Compiler) visitSupports(node *SupportsNode) {
	if !node.HasOutput() {
		return
	}
	c.buf += c.out(c.indent() + "@supports ")
	c.isCondition = true
	c.buf += c.out(c.visit(node.Condition))
	c.isCondition = false
	if c.Compress {
		c.buf += c.out("{")
	} else {
		c.buf += c.out(" {\n")
	}
	c.indents++
	c.visit(node.Block)
	c.indents--
	nl := "\n"
	if c.Compress {
		nl = ""
	}
	c.buf += c.out(c.indent() + "}" + nl)
}

// jsToInt32 is JavaScript's `n | 0`.
func jsToInt32(n float64) float64 {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	t := math.Trunc(n)
	m := math.Mod(t, 4294967296)
	if m < 0 {
		m += 4294967296
	}
	if m >= 2147483648 {
		m -= 4294967296
	}
	return m
}

func (c *Compiler) visitUnit(unit *UnitNode) string {
	typ := unit.Type
	n := unit.Val
	isFloat := n != jsToInt32(n)

	if c.Compress {
		switch typ {
		case "%", "s", "ms", "deg", "fr":
		default:
			if n == 0 {
				return "0"
			}
		}
		if isFloat && n < 1 && n > -1 {
			return strings.Replace(numToString(n), "0.", ".", 1) + typ
		}
	}
	if isFloat {
		return numToString(toFixedNum(n, 15)) + typ
	}
	return numToString(n) + typ
}

func (c *Compiler) visitGroup(group *GroupNode) {
	var stack [][]*SelectorNode
	if c.keyframe {
		stack = nil
	} else {
		stack = c.stack
	}
	comma := ",\n"
	if c.Compress {
		comma = ","
	}
	var sels []*SelectorNode
	for _, s := range group.Nodes {
		sels = append(sels, s.(*SelectorNode))
	}
	stack = append(stack, sels)
	if !c.keyframe {
		c.stack = stack
	}

	if group.GetBlock().HasProperties() {
		selectors := compileSelectors(stack, false, c.indent())
		length := len(selectors)
		if length > 0 {
			if c.keyframe {
				comma = ", "
				if c.Compress {
					comma = ","
				}
			}
			for i, selector := range selectors {
				last := i == length-1
				if c.keyframe && i > 0 {
					selector = strings.TrimSpace(selector)
				}
				suffix := comma
				if last {
					suffix = ""
				}
				c.buf += c.out(selector + suffix)
			}
		} else {
			group.GetBlock().LacksRenderedSelectors = true
		}
	}

	c.visit(group.GetBlock())
	if !c.keyframe {
		c.stack = c.stack[:len(c.stack)-1]
	}
}

func (c *Compiler) visitCall(call *CallNode) string {
	c.isURL = call.Name == "url"
	parts := make([]string, len(call.Args.Nodes))
	for i, arg := range call.Args.Nodes {
		parts[i] = c.visit(arg)
	}
	sep := ", "
	if c.Compress {
		sep = ","
	}
	args := strings.Join(parts, sep)
	if c.isURL {
		args = "\"" + args + "\""
	}
	c.isURL = false
	return call.Name + "(" + args + ")"
}

func (c *Compiler) visitExpression(exprNodes []Node, isList bool) string {
	length := len(exprNodes)
	rendered := make([]string, length)
	for i, node := range exprNodes {
		rendered[i] = c.visit(node)
	}
	var buf []string
	for i, node := range rendered {
		last := i == length-1
		buf = append(buf, node)
		next := ""
		if i+1 < length {
			next = rendered[i+1]
		}
		if next == "/" || node == "/" {
			continue
		}
		if last {
			continue
		}
		space := " "
		if c.isURL || (c.isCondition && (next == ")" || node == "(")) {
			space = ""
		}
		if isList {
			if c.Compress {
				buf = append(buf, ",")
			} else {
				buf = append(buf, ", ")
			}
		} else {
			buf = append(buf, space)
		}
	}
	return strings.Join(buf, "")
}

func (c *Compiler) visitProperty(prop *PropertyNode) string {
	val := strings.TrimSpace(c.visit(prop.Expr))
	name := prop.Name
	if name == "" {
		var b strings.Builder
		for _, s := range prop.Segments {
			b.WriteString(s.String())
		}
		name = b.String()
	}
	term := ";"
	if c.Compress {
		if c.last {
			term = ""
		}
	}
	if name == "@apply" {
		return c.indent() + name + " " + val + term
	}
	sep := ": "
	if c.Compress {
		sep = ":"
	}
	return c.indent() + name + sep + val + term
}

func (c *Compiler) debugInfo(node Node) {
	filename := node.base().Filename
	path := filename
	if filename != "stdin" {
		if abs, err := filepath.Abs(filename); err == nil {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				path = resolved
			} else {
				path = abs
			}
		}
	}
	line := node.base().Lineno
	if sub := nodesOf(node); len(sub) > 0 {
		line = sub[0].base().Lineno
	}
	if line == 0 {
		line = 1
	}
	if c.Linenos {
		c.buf += "\n/* line " + numToString(float64(line)) + " : " + path + " */\n"
	}
	if c.Firebug {
		var b strings.Builder
		for _, r := range path {
			switch r {
			case '.', ':', '/':
				b.WriteString("\\" + string(r))
			case '\\':
				b.WriteString("\\/")
			default:
				b.WriteRune(r)
			}
		}
		c.buf += "\n@media -stylus-debug-info" +
			"{filename{font-family:file\\:\\/\\/" + b.String() +
			"}line{font-family:\\00003" + numToString(float64(line)) + "}}\n"
	}
}
