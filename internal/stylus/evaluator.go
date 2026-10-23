/*!
 * Stylus - Evaluator
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go. The original signalled `return` by throwing
 * the Return node and caught it where a function body ended; the port panics
 * with the same node and recovers in the same places.
 */

package stylus

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

/*
 * Scope, frame and stack (lib/stack).
 */

type Scope struct {
	locals map[string]Node
	order  []string
}

func newScope() *Scope { return &Scope{locals: map[string]Node{}} }

func (s *Scope) add(ident *IdentNode) {
	if _, ok := s.locals[ident.Name]; !ok {
		s.order = append(s.order, ident.Name)
	}
	s.locals[ident.Name] = ident.Val
}

func (s *Scope) lookup(name string) Node {
	if v, ok := s.locals[name]; ok {
		return v
	}
	return nil
}

type Frame struct {
	scope  *Scope
	block  *BlockNode
	parent *Frame
}

func newFrame(block *BlockNode) *Frame {
	f := &Frame{block: block}
	if block.Scope {
		f.scope = newScope()
	}
	return f
}

func (f *Frame) Scope() *Scope {
	if f.scope != nil {
		return f.scope
	}
	if f.parent != nil {
		return f.parent.Scope()
	}
	return nil
}

func (f *Frame) lookup(name string) Node {
	if s := f.Scope(); s != nil {
		return s.lookup(name)
	}
	return nil
}

type Stack struct {
	frames []*Frame
}

func (s *Stack) push(f *Frame) {
	if len(s.frames) > 0 {
		f.parent = s.frames[len(s.frames)-1]
	}
	s.frames = append(s.frames, f)
}

func (s *Stack) pop() { s.frames = s.frames[:len(s.frames)-1] }

func (s *Stack) currentFrame() *Frame {
	if len(s.frames) == 0 {
		return nil
	}
	return s.frames[len(s.frames)-1]
}

func (s *Stack) getBlockFrame(block *BlockNode) *Frame {
	for _, f := range s.frames {
		if f.block == block {
			return f
		}
	}
	return nil
}

func (s *Stack) lookup(name string) Node {
	block := s.currentFrame().block
	for block != nil {
		if frame := s.getBlockFrame(block); frame != nil {
			if val := frame.lookup(name); val != nil {
				return val
			}
		}
		block = block.Parent
	}
	return nil
}

func (s *Stack) String() string {
	var buf []string
	for i := len(s.frames) - 1; i >= 0; i-- {
		block := s.frames[i].block
		node := block.NodeOf
		if node == nil {
			continue
		}
		location := "(" + node.base().Filename + ":" + numToString(float64(node.base().Lineno+1)) +
			":" + numToString(float64(node.base().Column)) + ")"
		switch n := node.(type) {
		case *FunctionNode:
			buf = append(buf, "    at "+n.Name+"() "+location)
		case *GroupNode:
			val := ""
			if len(n.Nodes) > 0 {
				if sel, ok := n.Nodes[0].(*SelectorNode); ok {
					val = sel.Val
				}
			}
			buf = append(buf, "    at \""+val+"\" "+location)
		}
	}
	return strings.Join(buf, "\n")
}

/*
 * Evaluator
 */

// Evaluator walks the parse tree and resolves everything but the CSS layout.
type Evaluator struct {
	root           Node
	Functions      map[string]*FunctionNode
	stack          *Stack
	imports        []string
	globals        map[string]Node
	paths          []string
	prefix         string
	filename       string
	includeCSS     bool
	resolveURL     bool
	resolveNocheck bool
	warnings       bool
	options        *Options
	calling        []string
	importStack    []string
	requireHistory map[string]bool
	returnDepth    int
	property       *PropertyNode
	ignoreColors   bool
	selectorCache  string
	global         *Frame
	selectorsMap   map[string][]*GroupNode
	Warn           func(string)
}

// NewEvaluator builds an evaluator for `root`.
func NewEvaluator(root Node, opts *Options) *Evaluator {
	if opts == nil {
		opts = &Options{}
	}
	e := &Evaluator{
		root:           root,
		Functions:      map[string]*FunctionNode{},
		stack:          &Stack{},
		imports:        opts.Imports,
		globals:        opts.Globals,
		paths:          append([]string{}, opts.Paths...),
		prefix:         opts.Prefix,
		filename:       opts.Filename,
		includeCSS:     opts.IncludeCSS,
		resolveURL:     opts.ResolveURL,
		resolveNocheck: opts.ResolveURLNocheck,
		warnings:       opts.Warn,
		options:        opts,
		requireHistory: map[string]bool{},
	}
	if e.globals == nil {
		e.globals = map[string]Node{}
	}
	fn := opts.Filename
	if fn == "" {
		fn = "."
	}
	e.paths = append(e.paths, path.Dir(fn))
	block := NewBlock(nil, nil)
	if rootBlock, ok := root.(*RootNode); ok {
		block = &rootBlock.BlockNode
	}
	e.global = newFrame(block)
	e.stack.push(e.global)
	return e
}

func (e *Evaluator) warn(msg string) {
	if !e.warnings {
		return
	}
	if e.Warn != nil {
		e.Warn("\u001b[33mWarning:\u001b[0m " + msg)
	}
}

// Evaluate runs the pass.
func (e *Evaluator) Evaluate() Node {
	e.setup()
	return e.visit(e.root)
}

func (e *Evaluator) setup() {
	e.populateGlobalScope()
	root, ok := e.root.(*RootNode)
	if !ok {
		return
	}
	var imports []Node
	for _, file := range e.imports {
		expr := NewExpression(false)
		expr.Push(NewString(file, ""))
		imports = append(imports, NewImport(expr, false))
	}
	root.Nodes = append(imports, root.Nodes...)
}

func (e *Evaluator) populateGlobalScope() {
	scope := e.global.Scope()
	for _, name := range colorNames {
		c := cssColors[name]
		rgba := NewRGBA(c[0], c[1], c[2], c[3])
		rgba.Name = name
		scope.add(NewIdent(name, rgba, false))
	}
	scope.add(NewIdent("embedurl", NewBuiltin("embedurl", bifEmbedURL, []string{"url"}, true), false))
	for _, name := range sortedKeys(e.globals) {
		scope.add(NewIdent(name, e.globals[name], false))
	}
}

func sortedKeys(m map[string]Node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// returnSignal carries a `return` out of a function body.
type returnSignal struct{ ret *ReturnNode }

func (e *Evaluator) visit(node Node) (result Node) {
	if node == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			if _, isReturn := r.(*returnSignal); isReturn {
				panic(r)
			}
			// A fault inside the evaluator is reported the way the original
			// reported a JavaScript TypeError: as a Stylus error carrying the
			// node's position, not as a Go stack trace.
			if _, isJS := r.(*JSError); !isJS {
				r = &JSError{Name: "TypeError", Message: fmt.Sprint(r)}
			}
			if err, ok := r.(*JSError); ok {
				if err.Filename == "" {
					err.Lineno = node.base().Lineno
					err.Column = node.base().Column
					err.Filename = node.base().Filename
					err.HasPosition = true
					err.StylusStack = e.stack.String()
					if data, rerr := os.ReadFile(err.Filename); rerr == nil {
						err.Input = string(data)
					}
				}
			}
			panic(r)
		}
	}()
	return e.dispatch(node)
}

func (e *Evaluator) dispatch(node Node) Node {
	switch n := node.(type) {
	case *RootNode:
		return e.visitRoot(n)
	case *BlockNode:
		return e.visitBlock(n)
	case *GroupNode:
		return e.visitGroup(n)
	case *ReturnNode:
		n.Expr = e.visit(n.Expr)
		panic(&returnSignal{n})
	case *MediaNode:
		n.Block = e.visit(n.Block).(*BlockNode)
		n.Val = e.visit(n.Val)
		return n
	case *QueryListNode:
		return e.visitQueryList(n)
	case *QueryNode:
		return e.visitQuery(n)
	case *FeatureNode:
		return e.visitFeature(n)
	case *ObjectNode:
		for _, k := range n.Vals.order {
			v, _ := n.Vals.get(k)
			n.Vals.set(k, e.visit(v))
		}
		return n
	case *MemberNode:
		return e.visitMember(n)
	case *KeyframesNode:
		return e.visitKeyframes(n)
	case *FunctionNode:
		return e.visitFunction(n)
	case *EachNode:
		return e.visitEach(n)
	case *CallNode:
		return e.visitCall(n)
	case *IdentNode:
		return e.visitIdent(n)
	case *BinOpNode:
		return e.visitBinOp(n)
	case *UnaryOpNode:
		return e.visitUnaryOp(n)
	case *TernaryNode:
		return e.visitTernary(n)
	case *ExpressionNode:
		return e.visitExpression(n)
	case *ArgumentsNode:
		return e.visitArguments(n)
	case *PropertyNode:
		return e.visitProperty(n)
	case *AtblockNode:
		n.Block = e.visit(n.Block).(*BlockNode)
		return n
	case *SupportsNode:
		return e.visitSupports(n)
	case *AtruleNode:
		n.ValStr = e.interpolate(n)
		if n.Block != nil {
			n.Block = e.visit(n.Block).(*BlockNode)
		}
		return n
	case *IfNode:
		return e.visitIf(n)
	case *ExtendNode:
		return e.visitExtend(n)
	case *ImportNode:
		return e.visitImport(n)
	}
	return node
}

func (e *Evaluator) visitRoot(block *RootNode) Node {
	if Node(block) != e.root {
		// The original swapped the cached import's constructor to Block and
		// visited the same object.
		return e.visit(&block.BlockNode)
	}
	for i := 0; i < len(block.Nodes); i++ {
		block.Index = i
		gen, idx := block.NodesGen, i
		res := e.visit(block.Nodes[idx])
		if block.NodesGen == gen && idx < len(block.Nodes) {
			block.Nodes[idx] = res
		}
	}
	return block
}

func (e *Evaluator) visitBlock(block *BlockNode) Node {
	e.stack.push(newFrame(block))
	for block.Index = 0; block.Index < len(block.Nodes); block.Index++ {
		// The original assigned through `block.nodes[block.index]`, whose
		// target is resolved before the visit runs; a mixin that replaces
		// block.nodes then writes into the array that was left behind.
		gen, idx := block.NodesGen, block.Index
		target := block.Nodes
		var res Node
		caught := func() (sig *returnSignal) {
			defer func() {
				if r := recover(); r != nil {
					if rs, ok := r.(*returnSignal); ok {
						sig = rs
						return
					}
					panic(r)
				}
			}()
			res = e.visit(target[idx])
			return nil
		}()
		if caught != nil {
			if e.returnDepth > 0 {
				e.stack.pop()
				panic(caught)
			}
			if block.Index < len(block.Nodes) {
				block.Nodes[block.Index] = caught.ret
			}
			break
		}
		if block.NodesGen == gen && idx < len(block.Nodes) {
			block.Nodes[idx] = res
		}
		_ = target
	}
	e.stack.pop()
	return block
}

func (e *Evaluator) visitGroup(group *GroupNode) Node {
	for _, s := range group.Nodes {
		if sel, ok := s.(*SelectorNode); ok {
			sel.Val = e.interpolate(sel)
		}
	}
	group.SetBlock(e.visit(group.GetBlock()).(*BlockNode))
	return group
}

func (e *Evaluator) visitQueryList(queries *QueryListNode) Node {
	for _, q := range queries.Nodes {
		e.visit(q)
	}
	if len(queries.Nodes) == 1 {
		query := queries.Nodes[0].(*QueryNode)
		if val := e.lookup(jsToStr(query.Type)); val != nil {
			s := stringOf(val.First())
			if s == "" {
				return queries
			}
			parser := NewParser(s, e.parserOptions())
			return e.visit(parser.queries())
		}
	}
	return queries
}

func (e *Evaluator) visitQuery(node *QueryNode) Node {
	if n, ok := node.Predicate.(Node); ok {
		node.Predicate = e.visit(n)
	}
	if n, ok := node.Type.(Node); ok {
		node.Type = e.visit(n)
	}
	for _, f := range node.Nodes {
		e.visit(f)
	}
	return node
}

func (e *Evaluator) visitFeature(node *FeatureNode) Node {
	node.Name = e.interpolate(node)
	if node.Expr != nil {
		e.returnDepth++
		node.Expr = e.visit(node.Expr)
		e.returnDepth--
	}
	return node
}

func (e *Evaluator) visitMember(node *MemberNode) Node {
	left := node.Left
	right := node.Right.(*IdentNode)
	obj, ok := e.visit(left).First().(*ObjectNode)
	if !ok {
		throwf("%s has no property .%s", left.String(), right.String())
	}
	if node.Val != nil {
		e.returnDepth++
		obj.SetValue(right.Name, e.visit(node.Val))
		e.returnDepth--
	}
	return obj.Get(right.Name)
}

func (e *Evaluator) visitKeyframes(keyframes *KeyframesNode) Node {
	if keyframes.Fabricated {
		return keyframes
	}
	keyframes.ValStr = strings.TrimSpace(e.interpolate(keyframes))
	if val := e.lookup(keyframes.ValStr); val != nil {
		first := val.First()
		s := stringOf(first)
		if s == "" {
			s = nameOf(first)
		}
		keyframes.ValStr = s
	}
	keyframes.Block = e.visit(keyframes.Block).(*BlockNode)
	if keyframes.Prefix != "official" {
		return keyframes
	}
	for _, prefix := range e.vendors() {
		if prefix == "ms" {
			continue
		}
		node := keyframes.CloneNode(nil, nil).(*KeyframesNode)
		node.ValStr = keyframes.ValStr
		node.Prefix = prefix
		node.Block = keyframes.Block
		node.Fabricated = true
		e.currentBlock().Push(node)
	}
	return NullNode_
}

func (e *Evaluator) visitFunction(fn *FunctionNode) Node {
	if local := e.stack.currentFrame().Scope().lookup(fn.Name); local != nil {
		e.warn("local " + local.NodeName() + " \"" + fn.Name + "\" previously defined in this scope")
	}
	if _, ok := e.Functions[fn.Name]; ok {
		e.warn("user-defined function \"" + fn.Name + "\" is already defined")
	}
	if _, ok := builtins[fn.Name]; ok {
		e.warn("built-in function \"" + fn.Name + "\" is already defined")
	}
	return fn
}

func (e *Evaluator) visitEach(each *EachNode) Node {
	e.returnDepth++
	expr := unwrap(e.visit(each.Expr))
	exprNodes := nodesOf(expr)
	val := NewIdent(each.Val, nil, false)
	keyName := each.Key
	if keyName == "" {
		keyName = "__index__"
	}
	key := NewIdent(keyName, nil, false)
	scope := e.currentScope()
	block := e.currentBlock()
	var vals []Node
	e.returnDepth--
	each.Block.Scope = false

	visitBody := func() {
		scope.add(val)
		scope.add(key)
		body := e.visit(each.Block.CloneBlock(nil, nil))
		vals = append(vals, nodesOf(body)...)
	}

	if len(exprNodes) == 1 && exprNodes[0].NodeName() == "object" {
		obj := exprNodes[0].(*ObjectNode)
		for _, prop := range obj.Vals.keysInOrder() {
			val.Val = NewString(prop, "")
			key.Val = obj.Get(prop)
			visitBody()
		}
	} else {
		for i := 0; i < len(exprNodes); i++ {
			val.Val = exprNodes[i]
			key.Val = NewUnit(float64(i), "")
			visitBody()
		}
	}

	e.mixin(vals, block)
	if len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return NullNode_
}

func (e *Evaluator) visitCall(call *CallNode) Node {
	var fn Node = e.lookup(call.Name)
	e.ignoreColors = call.Name == "url"

	if fn != nil && fn.NodeName() == "expression" {
		nodes := nodesOf(fn)
		if len(nodes) > 0 {
			fn = nodes[0]
		} else {
			fn = nil
		}
	}
	if fn != nil && fn.NodeName() != "function" {
		fn = e.lookupFunction(call.Name)
	}
	if fn == nil || fn.NodeName() != "function" {
		var ret Node
		if e.unvendorize(call.Name) == "calc" {
			if len(call.Args.Nodes) > 0 {
				ret = NewLiteral(call.Name + call.Args.Nodes[0].String())
			}
		} else {
			ret = e.literalCall(call)
		}
		e.ignoreColors = false
		if ret == nil {
			return NullNode_
		}
		return ret
	}

	e.calling = append(e.calling, call.Name)
	if len(e.calling) > 200 {
		throwNamed("RangeError", "Maximum stylus call stack size exceeded")
	}

	fnNode := fn.(*FunctionNode)
	e.returnDepth++
	visited := e.visit(call.Args)
	args, isArgs := visited.(*ArgumentsNode)
	if isArgs {
		for _, key := range args.Map.order {
			v, _ := args.Map.get(key)
			args.Map.set(key, e.visit(v.CloneNode(nil, nil)))
		}
	} else {
		// visitExpression cast the argument list to a single value.
		expr := NewExpression(false)
		expr.Push(visited)
		args = ArgumentsFromExpression(expr)
	}
	e.returnDepth--

	var ret Node
	if fnNode.Fn != nil {
		ret = e.invokeBuiltin(fnNode, args)
	} else {
		if call.Block != nil {
			call.Block = e.visit(call.Block).(*BlockNode)
		}
		ret = e.invokeFunction(fnNode, args, call.Block)
	}
	e.calling = e.calling[:len(e.calling)-1]
	e.ignoreColors = false
	return ret
}

func (e *Evaluator) visitIdent(ident *IdentNode) Node {
	if ident.Property {
		if prop := e.lookupProperty(ident.Name); prop != nil {
			return e.visit(prop.Expr.CloneNode(nil, nil))
		}
		return NullNode_
	}
	if _, isNull := ident.Val.(*NullNode); isNull {
		val := e.lookup(ident.Name)
		if val != nil && ident.Mixin {
			e.mixinNode(val)
		}
		if val != nil {
			return e.visit(val)
		}
		return ident
	}
	e.returnDepth++
	ident.Val = e.visit(ident.Val)
	e.returnDepth--
	e.currentScope().add(ident)
	return ident.Val
}

func (e *Evaluator) visitBinOp(binop *BinOpNode) Node {
	if binop.Op == "is defined" {
		return e.isDefined(binop.Left)
	}
	e.returnDepth++
	op := binop.Op
	left := e.visit(binop.Left)
	var right Node
	if op == "||" || op == "&&" {
		right = binop.Right
	} else {
		right = e.visit(binop.Right)
	}
	var val Node
	if binop.Val != nil {
		val = e.visit(binop.Val)
	}
	e.returnDepth--

	var result Node
	err := catch(func() {
		result = e.visit(left.Operate(op, right, val))
	})
	if err != nil {
		if err.Name == "CoercionError" {
			switch op {
			case "==":
				return FalseNode
			case "!=":
				return TrueNode
			}
		}
		panic(err)
	}
	return result
}

func (e *Evaluator) visitUnaryOp(unary *UnaryOpNode) Node {
	op := unary.Op
	node := e.visit(unary.Expr)
	if op != "!" {
		node = node.First().CloneNode(nil, nil)
		assertType(node, "unit", "")
	}
	switch op {
	case "-":
		u := node.(*UnitNode)
		u.Val = -u.Val
	case "+":
		// unary plus leaves the value unchanged
	case "~":
		u := node.(*UnitNode)
		u.Val = float64(^int32(jsToInt32(u.Val)))
	case "!":
		return node.ToBoolean().Negate()
	}
	return node
}

func (e *Evaluator) visitTernary(ternary *TernaryNode) Node {
	ok := e.visit(ternary.Cond).ToBoolean()
	if ok.Val {
		return e.visit(ternary.TrueExpr)
	}
	return e.visit(ternary.FalseExpr)
}

func (e *Evaluator) visitExpression(expr *ExpressionNode) Node {
	for i := range expr.Nodes {
		expr.Nodes[i] = e.visit(expr.Nodes[i])
	}
	if e.castable(expr) {
		return e.cast(expr)
	}
	return expr
}

func (e *Evaluator) visitArguments(args *ArgumentsNode) Node {
	for i := range args.Nodes {
		args.Nodes[i] = e.visit(args.Nodes[i])
	}
	// The original used visitExpression for Arguments too, so an argument list
	// of "<unit> <unit-name>" is cast to a single unit.
	if e.castable(&args.ExpressionNode) {
		return e.cast(&args.ExpressionNode)
	}
	return args
}

func (e *Evaluator) visitProperty(prop *PropertyNode) Node {
	name := e.interpolate(prop)
	fn := e.lookup(name)
	call := fn != nil && fn.First().NodeName() == "function"
	literal := false
	for _, c := range e.calling {
		if c == name {
			literal = true
			break
		}
	}
	prevProp := e.property

	if call && !literal && !prop.Literal {
		args := ArgumentsFromExpression(toExpressionNode(unwrap(prop.Expr.CloneNode(nil, nil))))
		prop.Name = name
		e.property = prop
		e.returnDepth++
		e.property.Expr = e.visit(prop.Expr)
		e.returnDepth--
		ret := e.visit(NewCall(name, args))
		e.property = prevProp
		return ret
	}
	e.returnDepth++
	prop.Name = name
	prop.Literal = true
	e.property = prop
	prop.Expr = e.visit(prop.Expr)
	e.property = prevProp
	e.returnDepth--
	return prop
}

func toExpressionNode(n Node) *ExpressionNode {
	switch v := n.(type) {
	case *ExpressionNode:
		return v
	case *ArgumentsNode:
		return &v.ExpressionNode
	}
	return n.ToExpression()
}

func (e *Evaluator) visitSupports(node *SupportsNode) Node {
	condition := node.Condition
	e.returnDepth++
	node.Condition = e.visit(condition)
	e.returnDepth--
	if expr, ok := condition.(*ExpressionNode); ok && len(expr.Nodes) == 1 {
		if s, ok := expr.First().(*StringNode); ok {
			node.Condition = NewLiteral(s.Str)
		}
	}
	node.Block = e.visit(node.Block).(*BlockNode)
	return node
}

func (e *Evaluator) visitIf(node *IfNode) Node {
	block := e.currentBlock()
	negate := node.Negate
	e.returnDepth++
	ok := e.visit(node.Cond).First().ToBoolean()
	e.returnDepth--
	if b, isBlock := node.Block.(*BlockNode); isBlock {
		b.Scope = b.HasMedia()
	}

	var ret Node
	if negate {
		if !ok.Val {
			ret = e.visit(node.Block)
		}
	} else if ok.Val {
		ret = e.visit(node.Block)
	} else if len(node.Elses) > 0 {
		for _, els := range node.Elses {
			if elseIf, isIf := els.(*IfNode); isIf && elseIf.Cond != nil {
				if b, isBlock := elseIf.Block.(*BlockNode); isBlock {
					b.Scope = b.HasMedia()
				}
				e.returnDepth++
				cond := e.visit(elseIf.Cond).First().ToBoolean()
				e.returnDepth--
				if cond.Val {
					ret = e.visit(elseIf.Block)
					break
				}
			} else {
				if b, isBlock := els.(*BlockNode); isBlock {
					b.Scope = b.HasMedia()
				}
				ret = e.visit(els)
			}
		}
	}

	if ret != nil && !node.Postfix && block != nil && block.NodeOf != nil {
		switch block.NodeOf.NodeName() {
		case "group", "atrule", "media", "supports", "keyframes":
			e.mixin(nodesOf(ret), block)
			return NullNode_
		}
	}
	if ret == nil {
		return NullNode_
	}
	return ret
}

func (e *Evaluator) visitExtend(extend *ExtendNode) Node {
	block := e.currentBlock()
	if block.NodeOf == nil || block.NodeOf.NodeName() != "group" {
		block = e.closestGroup()
	}
	if block == nil || block.NodeOf == nil {
		throwf("invalid @extend outside of a selector group")
	}
	group := block.NodeOf.(*GroupNode)
	for _, selector := range extend.SelectorNodes {
		clone := selector.CloneNode(nil, nil).(*SelectorNode)
		group.Extends = append(group.Extends, ExtendSel{
			Selector: strings.TrimSpace(e.interpolate(clone)),
			Optional: selector.Optional,
			Lineno:   selector.Lineno,
			Column:   selector.Column,
		})
	}
	return NullNode_
}

var reURLOrHash = regexp.MustCompile(`(?i)^(?:url\s*\(\s*)?['"]?(?:#|(?:https?:)?//)`)
var reCSSExt = regexp.MustCompile(`\.css(?:"|$)`)
var reStylExt = regexp.MustCompile(`(?i)\.styl`)
var reStylSuffix = regexp.MustCompile(`(?i)\.styl$`)

func (e *Evaluator) visitImport(imported *ImportNode) Node {
	e.returnDepth++
	p := e.visit(imported.Path).First()
	nodeName := "import"
	if imported.Once {
		nodeName = "require"
	}
	e.returnDepth--

	if call, ok := p.(*CallNode); ok && call.Name == "url" {
		if imported.Once {
			throwf("You cannot @require a url")
		}
		return imported
	}
	pathStr := stringOf(p)
	if pathStr == "" {
		throwf("@%s string expected", nodeName)
	}
	name := pathStr
	literal := false

	// The core library is compiled into the binary rather than read from disk.
	if pathStr == coreLibraryPath {
		block := NewBlock(nil, nil)
		block.Push(e.importSource(coreLibrary, coreLibraryPath))
		return block
	}

	if reURLOrHash.MatchString(pathStr) {
		if imported.Once {
			throwf("You cannot @require a url")
		}
		return imported
	}
	if reCSSExt.MatchString(pathStr) {
		literal = true
		if !imported.Once && !e.includeCSS {
			return imported
		}
	}
	if !literal && !reStylSuffix.MatchString(pathStr) {
		pathStr += ".styl"
	}

	found := find(pathStr, e.paths, e.filename)
	if found == nil {
		found = lookupIndex(name, e.paths, e.filename)
	}
	if found == nil {
		throwf("failed to locate @%s file %s", nodeName, pathStr)
	}

	block := NewBlock(nil, nil)
	for _, f := range found {
		block.Push(e.importFile(imported, f, literal))
	}
	return block
}

// importSource parses and evaluates `str` as an imported stylesheet.
func (e *Evaluator) importSource(str, file string) Node {
	e.paths = append(e.paths, filepath.Dir(file))
	e.importStack = append(e.importStack, file)
	curFilename = file
	opts := e.parserOptions()
	block := NewBlock(nil, nil)
	opts.Root = block
	parser := NewParser(str, opts)
	var parsed Node
	perr := catch(func() { parsed = parser.Parse() })
	if perr != nil {
		perr.Filename = file
		if parser.lexer != nil {
			perr.Lineno = parser.lexer.Lineno
			perr.Column = parser.lexer.Column
		}
		perr.Input = str
		perr.HasPosition = true
		panic(perr)
	}
	if b, ok := parsed.(*BlockNode); ok {
		block = b
	}
	cur := e.currentBlock()
	cloned := block.CloneBlock(cur, nil)
	cloned.Parent = cur
	cloned.Scope = false
	ret := e.visit(cloned)
	e.importStack = e.importStack[:len(e.importStack)-1]
	if !e.resolveURL || e.resolveNocheck {
		e.paths = e.paths[:len(e.paths)-1]
	}
	return ret
}

func (e *Evaluator) importFile(node *ImportNode, file string, literal bool) Node {
	if node.Once {
		if e.requireHistory[file] {
			return NullNode_
		}
		e.requireHistory[file] = true
		if literal && !e.includeCSS {
			return node
		}
	}
	for _, f := range e.importStack {
		if f == file {
			throwf("import loop has been found")
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		throwf("failed to read %s", file)
	}
	str := string(data)
	if strings.TrimSpace(str) == "" {
		return NullNode_
	}
	dir := filepath.Dir(file)
	e.paths = append(e.paths, dir)

	e.importStack = append(e.importStack, file)
	curFilename = file

	var literalNode *LiteralNode
	if literal {
		literalNode = NewLiteral(normaliseNewlines(str))
		literalNode.Lineno = 1
		literalNode.Column = 1
		if !e.resolveURL {
			e.importStack = e.importStack[:len(e.importStack)-1]
			return literalNode
		}
	}

	block := NewBlock(nil, nil)
	opts := e.parserOptions()
	opts.Root = block
	parser := NewParser(str, opts)
	var parsed Node
	perr := catch(func() { parsed = parser.Parse() })
	if perr != nil {
		line, column := 0, 0
		if parser.lexer != nil {
			line, column = parser.lexer.Lineno, parser.lexer.Column
		}
		if literal && e.includeCSS && e.resolveURL {
			e.warn("ParseError: " + file + ":" + numToString(float64(line)) + ":" +
				numToString(float64(column)) + ". This file included as-is")
			e.importStack = e.importStack[:len(e.importStack)-1]
			return literalNode
		}
		perr.Filename = file
		perr.Lineno = line
		perr.Column = column
		perr.Input = str
		perr.HasPosition = true
		panic(perr)
	}
	if b, ok := parsed.(*BlockNode); ok {
		block = b
	}

	cur := e.currentBlock()
	cloned := block.CloneBlock(cur, nil)
	cloned.Parent = cur
	cloned.Scope = false
	ret := e.visit(cloned)
	e.importStack = e.importStack[:len(e.importStack)-1]
	if !e.resolveURL || e.resolveNocheck {
		e.paths = e.paths[:len(e.paths)-1]
	}
	return ret
}

func (e *Evaluator) parserOptions() ParserOptions {
	return ParserOptions{Prefix: e.prefix}
}

func (e *Evaluator) invokeFunction(fn *FunctionNode, args *ArgumentsNode, content *BlockNode) Node {
	block := NewBlock(fn.Block.Parent, nil)
	body := fn.Block.CloneBlock(block, nil)
	mixinBlock := e.stack.currentFrame().block
	e.stack.push(newFrame(block))
	scope := e.currentScope()

	scope.add(NewIdent("arguments", args, false))
	if e.returnDepth > 0 {
		scope.add(NewIdent("mixin", FalseNode, false))
	} else {
		name := "root"
		if mixinBlock != nil {
			name = blockNodeName(mixinBlock)
		}
		scope.add(NewIdent("mixin", NewString(name, ""), false))
	}
	if e.property != nil {
		scope.add(NewIdent("current-property", e.propertyExpression(e.property, fn.Name), false))
	} else {
		scope.add(NewIdent("current-property", NullNode_, false))
	}
	expr := NewExpression(false)
	for i := len(e.calling) - 1; i > 0; i-- {
		expr.Push(NewLiteral(e.calling[i-1]))
	}
	scope.add(NewIdent("called-from", expr, false))

	i := 0
	length := len(args.Nodes)
	for _, pnode := range fn.Params.Nodes {
		node := pnode.(*IdentNode)
		if node.Rest {
			val := NewExpression(false)
			for ; i < length; i++ {
				val.Push(args.Nodes[i])
			}
			val.Preserve = true
			val.IsList = args.IsList
			node.Val = val
			scope.add(node)
			continue
		}
		var arg Node
		if v, ok := args.Map.get(node.Name); ok {
			arg = v
		} else if i < len(args.Nodes) {
			arg = args.Nodes[i]
			i++
		} else {
			i++
		}
		node = node.CloneNode(nil, nil).(*IdentNode)
		if arg != nil {
			if isEmptyNode(arg) {
				if i-1 >= 0 && i-1 < len(args.Nodes) {
					args.Nodes[i-1] = e.visit(node)
				}
			} else {
				node.Val = arg
			}
		} else {
			args.Push(node.Val)
		}
		if _, isNull := node.Val.(*NullNode); isNull {
			throwf("argument \"%s\" required for %s", node.String(), fn.String())
		}
		scope.add(node)
	}

	if content != nil {
		scope.add(NewIdent("block", content, true))
	}
	return e.invoke(body, true, fn.Filename)
}

func isEmptyNode(n Node) bool {
	switch v := n.(type) {
	case *ExpressionNode:
		return v.IsEmpty()
	case *ArgumentsNode:
		return v.IsEmpty()
	}
	return false
}

func (e *Evaluator) invokeBuiltin(fn *FunctionNode, args *ArgumentsNode) Node {
	var list []Node
	if fn.Raw {
		list = args.Nodes
	} else {
		nodesLeft := append([]Node{}, args.Nodes...)
		for _, param := range fn.FnParams {
			var arg Node
			if v, ok := args.Map.get(param); ok {
				arg = v
			} else if len(nodesLeft) > 0 {
				arg = nodesLeft[0]
				nodesLeft = nodesLeft[1:]
			}
			if arg == nil {
				continue
			}
			arg = unwrap(arg)
			sub := nodesOf(arg)
			if len(sub) > 1 {
				for _, s := range sub {
					list = append(list, unwrap(s.First()))
				}
			} else {
				list = append(list, arg.First())
			}
		}
	}
	body := fn.Fn(e, list)
	if body == nil {
		body = NullNode_
	}
	expr := NewExpression(false)
	expr.Push(body)
	return e.invoke(expr, false, "")
}

func (e *Evaluator) invoke(body Node, stack bool, filename string) Node {
	if filename != "" {
		e.paths = append(e.paths, filepath.Dir(filename))
	}
	var ret Node
	if e.returnDepth > 0 {
		ret = e.eval(nodesOf(body))
		if stack {
			e.stack.pop()
		}
	} else {
		body = e.visit(body)
		if stack {
			e.stack.pop()
		}
		e.mixin(nodesOf(body), e.currentBlock())
		ret = NullNode_
	}
	if filename != "" {
		e.paths = e.paths[:len(e.paths)-1]
	}
	return ret
}

func (e *Evaluator) mixin(items []Node, block *BlockNode) {
	if len(items) == 0 || block == nil {
		return
	}
	length := len(block.Nodes)
	idx := block.Index
	if idx > length {
		idx = length
	}
	head := append([]Node{}, block.Nodes[:idx]...)
	var tail []Node
	if idx+1 <= length {
		tail = append([]Node{}, block.Nodes[idx+1:length]...)
	}
	head = e.mixinInto(items, head, block)
	block.Index = 0
	block.Nodes = append(head, tail...)
	block.NodesGen++
}

func (e *Evaluator) mixinInto(items []Node, dest []Node, block *BlockNode) []Node {
	for _, node := range items {
		switch n := node.(type) {
		case *ReturnNode:
			return dest
		case *BlockNode:
			dest = e.mixinInto(n.Nodes, dest, block)
			continue
		case *MediaNode:
			if n.Block != nil && n.Block.Parent != nil && n.Block.Parent.NodeOf != nil &&
				n.Block.Parent.NodeOf.NodeName() != "call" {
				n.Block.Parent = block
			}
			dest = append(dest, node)
		case *PropertyNode:
			val := n.Expr
			if n.Literal && val != nil {
				if id, ok := val.First().(*IdentNode); ok && id.Name == "block" {
					unwrapped := unwrap(val)
					if sub := nodesOf(unwrapped); len(sub) > 0 {
						sub[0] = NewLiteral("block")
					}
				}
			}
			dest = append(dest, node)
		default:
			dest = append(dest, node)
		}
	}
	return dest
}

func (e *Evaluator) mixinNode(node Node) Node {
	visited := e.visit(node.First())
	switch v := visited.(type) {
	case *ObjectNode:
		e.mixinObject(v)
		return NullNode_
	case *BlockNode:
		e.mixin(v.Nodes, e.currentBlock())
		return NullNode_
	case *AtblockNode:
		e.mixin(nodesOf(v), e.currentBlock())
		return NullNode_
	}
	return nil
}

func (e *Evaluator) mixinObject(object *ObjectNode) {
	str := "$block " + object.ToBlock()
	opts := e.parserOptions()
	parsed0 := NewBlock(nil, nil)
	opts.Root = parsed0
	parser := NewParser(str, opts)
	var parsed Node
	perr := catch(func() { parsed = parser.Parse() })
	if perr != nil {
		perr.Filename = e.filename
		if parser.lexer != nil {
			perr.Lineno = parser.lexer.Lineno
			perr.Column = parser.lexer.Column
		}
		perr.Input = str
		perr.HasPosition = true
		panic(perr)
	}
	block := parsed0
	if b, ok := parsed.(*BlockNode); ok {
		block = b
	}
	if r, ok := e.root.(*RootNode); ok {
		block.Parent = &r.BlockNode
	}
	block.Scope = false
	ret := e.visit(block)
	vals := nodesOf(ret.First())
	for _, v := range vals {
		if b := blockOf(v); b != nil {
			e.mixin(b.Nodes, e.currentBlock())
			break
		}
	}
}

func (e *Evaluator) eval(vals []Node) (result Node) {
	if vals == nil {
		return NullNode_
	}
	var node Node = NullNode_
	caught := func() (sig *returnSignal) {
		defer func() {
			if r := recover(); r != nil {
				if rs, ok := r.(*returnSignal); ok {
					sig = rs
					return
				}
				panic(r)
			}
		}()
		for i := 0; i < len(vals); i++ {
			node = vals[i]
			switch n := node.(type) {
			case *IfNode:
				if _, isBlock := n.Block.(*BlockNode); !isBlock {
					node = e.visit(node)
					break
				}
				node = e.visit(node)
				if sub := nodesOf(node); sub != nil {
					node = e.eval(sub)
				}
			case *EachNode, *BlockNode:
				node = e.visit(node)
				if sub := nodesOf(node); sub != nil {
					node = e.eval(sub)
				}
			default:
				node = e.visit(node)
			}
		}
		return nil
	}()
	if caught != nil {
		return caught.ret.Expr
	}
	return node
}

func (e *Evaluator) literalCall(call *CallNode) Node {
	call.Args = e.visit(call.Args).(*ArgumentsNode)
	return call
}

func (e *Evaluator) lookupProperty(name string) *PropertyNode {
	i := len(e.stack.frames)
	index := e.currentBlock().Index
	top := i
	for i > 0 {
		i--
		block := e.stack.frames[i].block
		if block.NodeOf == nil {
			continue
		}
		switch block.NodeOf.NodeName() {
		case "group", "function", "if", "each", "atrule", "media", "atblock", "call":
			nodes := block.Nodes
			if i+1 == top {
				for index > 0 {
					index--
					if index >= len(nodes) {
						continue
					}
					if Node(e.property) == nodes[index] {
						continue
					}
					other := e.interpolate(nodes[index])
					if name == other {
						if prop, ok := nodes[index].CloneNode(nil, nil).(*PropertyNode); ok {
							return prop
						}
					}
				}
			} else {
				for l := len(nodes); l > 0; {
					l--
					if nodes[l].NodeName() != "property" || Node(e.property) == nodes[l] {
						continue
					}
					other := e.interpolate(nodes[l])
					if name == other {
						if prop, ok := nodes[l].CloneNode(nil, nil).(*PropertyNode); ok {
							return prop
						}
					}
				}
			}
		}
	}
	return nil
}

func (e *Evaluator) closestBlock() *BlockNode {
	for i := len(e.stack.frames) - 1; i >= 0; i-- {
		block := e.stack.frames[i].block
		if block.NodeOf != nil {
			switch block.NodeOf.NodeName() {
			case "group", "keyframes", "atrule", "atblock", "media", "call":
				return block
			}
		}
	}
	return nil
}

func (e *Evaluator) closestGroup() *BlockNode {
	for i := len(e.stack.frames) - 1; i >= 0; i-- {
		block := e.stack.frames[i].block
		if block.NodeOf != nil && block.NodeOf.NodeName() == "group" {
			return block
		}
	}
	return nil
}

func (e *Evaluator) selectorStack() [][]*SelectorNode {
	var stack [][]*SelectorNode
	for _, frame := range e.stack.frames {
		block := frame.block
		if block.NodeOf != nil && block.NodeOf.NodeName() == "group" {
			group := block.NodeOf.(*GroupNode)
			var sels []*SelectorNode
			for _, s := range group.Nodes {
				sel := s.(*SelectorNode)
				if sel.Val == "" {
					sel.Val = e.interpolate(sel)
				}
				sels = append(sels, sel)
			}
			stack = append(stack, sels)
		}
	}
	return stack
}

func (e *Evaluator) lookup(name string) Node {
	if e.ignoreColors {
		if _, ok := cssColors[name]; ok {
			return nil
		}
	}
	if val := e.stack.lookup(name); val != nil {
		return unwrap(val)
	}
	fn := e.lookupFunction(name)
	if fn == nil {
		return nil
	}
	return fn
}

func (e *Evaluator) interpolate(node Node) string {
	isSelector := node.NodeName() == "selector"
	var toStr func(Node) string
	toStr = func(n Node) string {
		switch v := n.(type) {
		case *FunctionNode:
			return v.Name
		case *IdentNode:
			return v.Name
		case *LiteralNode:
			if e.prefix != "" && !v.Prefixed {
				v.Val = prefixSelector(v.Val, e.prefix)
				v.Str = v.Val
				v.Prefixed = true
			}
			return v.Val
		case *StringNode:
			if e.prefix != "" && !v.Prefixed {
				v.Val = prefixSelector(v.Val, e.prefix)
				v.Str = v.Val
				v.Prefixed = true
			}
			return v.Val
		case *UnitNode:
			if v.Type == "%" {
				return numToString(v.Val) + "%"
			}
			return numToString(v.Val)
		case *MemberNode:
			return toStr(e.visit(v))
		case *ExpressionNode:
			for _, c := range e.calling {
				if c == "selector" && e.selectorCache != "" {
					return e.selectorCache
				}
			}
			e.returnDepth++
			ret := toStr(e.visit(v).First())
			e.returnDepth--
			if isSelector {
				e.selectorCache = ret
			}
			return ret
		}
		return ""
	}
	if segs := segmentsOf(node); segs != nil {
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(toStr(s))
		}
		return b.String()
	}
	return toStr(node)
}

// prefixSelector is replace(/\.(?=[\w-])|^\.$/g, '.' + prefix).
func prefixSelector(s, prefix string) string {
	var b strings.Builder
	if s == "." {
		return "." + prefix
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && i+1 < len(s) && (isWordByte(s[i+1]) || s[i+1] == '-') {
			b.WriteString("." + prefix)
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func segmentsOf(node Node) []Node {
	switch v := node.(type) {
	case *SelectorNode:
		return v.Segments
	case *PropertyNode:
		return v.Segments
	case *FeatureNode:
		return v.Segments
	case *AtruleNode:
		return v.Segments
	case *KeyframesNode:
		return v.Segments
	}
	return nil
}

func (e *Evaluator) lookupFunction(name string) *FunctionNode {
	if fn, ok := e.Functions[name]; ok {
		return fn
	}
	if bif, ok := builtins[name]; ok {
		return NewBuiltin(name, bif.fn, bif.params, bif.raw)
	}
	return nil
}

func (e *Evaluator) isDefined(node Node) Node {
	if id, ok := node.(*IdentNode); ok {
		return NewBoolean(e.lookup(id.Name) != nil)
	}
	throwf("invalid \"is defined\" check on non-variable %s", node.String())
	return nil
}

func (e *Evaluator) propertyExpression(prop *PropertyNode, name string) Node {
	expr := NewExpression(false)
	val := prop.Expr.CloneNode(nil, nil)
	expr.Push(NewString(prop.Name, ""))
	var replace func(Node) Node
	replace = func(node Node) Node {
		if call, ok := node.(*CallNode); ok && call.Name == name {
			return NewLiteral("__CALL__")
		}
		switch v := node.(type) {
		case *ExpressionNode:
			for i, c := range v.Nodes {
				v.Nodes[i] = replace(c)
			}
		case *ArgumentsNode:
			for i, c := range v.Nodes {
				v.Nodes[i] = replace(c)
			}
		}
		return node
	}
	val = replace(val)
	expr.Push(val)
	return expr
}

func (e *Evaluator) cast(expr *ExpressionNode) Node {
	return NewUnit(expr.First().(*UnitNode).Val, expr.Nodes[1].(*IdentNode).Name)
}

func (e *Evaluator) castable(expr *ExpressionNode) bool {
	if len(expr.Nodes) != 2 {
		return false
	}
	if expr.First().NodeName() != "unit" {
		return false
	}
	id, ok := expr.Nodes[1].(*IdentNode)
	return ok && cssUnits[id.Name]
}

func (e *Evaluator) currentBlock() *BlockNode {
	f := e.stack.currentFrame()
	if f == nil {
		return nil
	}
	return f.block
}

func (e *Evaluator) currentScope() *Scope { return e.stack.currentFrame().Scope() }

func (e *Evaluator) vendors() []string {
	val := e.lookup("vendors")
	if val == nil {
		return nil
	}
	var out []string
	for _, n := range nodesOf(val) {
		out = append(out, stringOf(n))
	}
	return out
}

func (e *Evaluator) unvendorize(prop string) string {
	for _, v := range e.vendors() {
		if v != "official" {
			vendor := "-" + v + "-"
			if strings.Contains(prop, vendor) {
				return strings.Replace(prop, vendor, "", 1)
			}
		}
	}
	return prop
}
