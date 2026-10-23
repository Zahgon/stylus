/*!
 * Stylus - Normalizer
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import "strings"

// Normalizer bubbles nested rules out and resolves @extend.
type Normalizer struct {
	root      Node
	hoist     bool
	stack     [][]*SelectorNode
	selMap    map[string][]*GroupNode
	imports   []Node
	charset   Node
	rootIndex int
	// srcRoot is the tree being walked; the original spliced hoisted medias
	// into it, so the walk visits them in place.
	srcRoot *RootNode
}

// NewNormalizer builds a normaliser for `root`.
func NewNormalizer(root Node, opts *Options) *Normalizer {
	n := &Normalizer{root: root, selMap: map[string][]*GroupNode{}}
	if opts != nil {
		n.hoist = opts.HoistAtrules
	}
	return n
}

// Normalize runs the pass.
func (n *Normalizer) Normalize() Node {
	ret := n.visit(n.root)
	if n.hoist {
		if r, ok := ret.(*RootNode); ok {
			if len(n.imports) > 0 {
				r.Nodes = append(append([]Node{}, n.imports...), r.Nodes...)
			}
			if n.charset != nil {
				r.Nodes = append([]Node{n.charset}, r.Nodes...)
			}
		}
	}
	return ret
}

func (n *Normalizer) visit(node Node) Node {
	switch v := node.(type) {
	case *RootNode:
		return n.visitRoot(v)
	case *PropertyNode:
		n.visit(v.Expr)
		return v
	case *ExpressionNode:
		return n.visitExpression(v)
	case *BlockNode:
		return n.visitBlock(v)
	case *GroupNode:
		return n.visitGroup(v)
	case *FunctionNode:
		return NullNode_
	case *MediaNode:
		return n.visitMedia(v)
	case *SupportsNode:
		n.bubble(&v.AtruleNode)
		return v
	case *KeyframesNode:
		return n.visitKeyframes(v)
	case *AtruleNode:
		if v.Block != nil {
			v.Block = n.visit(v.Block).(*BlockNode)
		}
		return v
	case *ImportNode:
		n.imports = append(n.imports, v)
		if n.hoist {
			return NullNode_
		}
		return v
	case *CharsetNode:
		n.charset = v
		if n.hoist {
			return NullNode_
		}
		return v
	}
	return node
}

func (n *Normalizer) bubble(node *AtruleNode) {
	var props []Node
	var other []Node

	var filterProps func(block *BlockNode)
	filterProps = func(block *BlockNode) {
		for _, child := range block.Nodes {
			visited := n.visit(child)
			switch visited.NodeName() {
			case "property":
				props = append(props, visited)
			case "block":
				filterProps(visited.(*BlockNode))
			default:
				other = append(other, visited)
			}
		}
	}
	filterProps(node.Block)

	if len(props) > 0 {
		selector := NewSelector([]Node{NewLiteral("&")})
		selector.Lineno = node.Lineno
		selector.Column = node.Column
		selector.Filename = node.Filename
		selector.Val = "&"
		group := NewGroup()
		group.Lineno = node.Lineno
		group.Column = node.Column
		group.Filename = node.Filename
		block := NewBlock(node.Block, group)
		block.Lineno = node.Lineno
		block.Column = node.Column
		block.Filename = node.Filename
		for _, prop := range props {
			block.Push(prop)
		}
		group.Push(selector)
		group.SetBlock(block)
		node.Block.Nodes = nil
		node.Block.Push(group)
		for _, o := range other {
			node.Block.Push(o)
		}
		if g := n.closestGroup(node.Block); g != nil {
			node.Group = g.CloneNode(nil, nil).(*GroupNode)
		}
		node.Bubbled = true
	}
}

func (n *Normalizer) closestGroup(block *BlockNode) *GroupNode {
	parent := block.Parent
	for parent != nil && parent.NodeOf != nil {
		node := parent.NodeOf
		if g, ok := node.(*GroupNode); ok {
			return g
		}
		if b := node.base().Block; b != nil {
			parent = b.Parent
		} else {
			parent = nil
		}
	}
	return nil
}

func (n *Normalizer) visitRoot(block *RootNode) Node {
	ret := NewRoot()
	n.srcRoot = block
	for i := 0; i < len(block.Nodes); i++ {
		node := block.Nodes[i]
		switch node.NodeName() {
		case "null", "expression", "function", "unit", "atblock":
			continue
		default:
			n.rootIndex = i
			ret.Push(n.visit(node))
		}
	}
	return ret
}

func (n *Normalizer) visitExpression(expr *ExpressionNode) Node {
	for i, node := range expr.Nodes {
		if node.NodeName() == "block" {
			literal := NewLiteral("block")
			literal.Lineno = expr.Lineno
			literal.Column = expr.Column
			expr.Nodes[i] = literal
		}
	}
	return expr
}

func (n *Normalizer) visitBlock(block *BlockNode) Node {
	if block.HasProperties() {
		for i := 0; i < len(block.Nodes); i++ {
			node := block.Nodes[i]
			switch node.NodeName() {
			case "null", "expression", "function", "group", "unit", "atblock":
				continue
			default:
				block.Nodes[i] = n.visit(node)
			}
		}
	}
	for i := 0; i < len(block.Nodes); i++ {
		block.Nodes[i] = n.visit(block.Nodes[i])
	}
	return block
}

func (n *Normalizer) visitGroup(group *GroupNode) Node {
	// Normalise interpolated selectors with a comma.
	origLen := len(group.Nodes)
	for idx := 0; idx < origLen && idx < len(group.Nodes); idx++ {
		selector, ok := group.Nodes[idx].(*SelectorNode)
		if !ok {
			continue
		}
		if !strings.Contains(selector.Val, ",") {
			continue
		}
		if strings.Contains(selector.Val, "\\,") {
			selector.Val = strings.ReplaceAll(selector.Val, "\\,", ",")
			continue
		}
		parts := strings.Split(selector.Val, ",")
		root := strings.HasPrefix(selector.Val, "/")
		i := idx
		for k, part := range parts {
			part = strings.TrimSpace(part)
			if root && k > 0 && !strings.Contains(part, "&") {
				part = "/" + part
			}
			s := NewSelector([]Node{NewLiteral(part)})
			s.Val = part
			s.Block = group.GetBlock()
			for len(group.Nodes) <= i {
				group.Nodes = append(group.Nodes, NullNode_)
			}
			group.Nodes[i] = s
			i++
		}
	}

	var sels []*SelectorNode
	for _, s := range group.Nodes {
		if sel, ok := s.(*SelectorNode); ok {
			sels = append(sels, sel)
		}
	}
	n.stack = append(n.stack, sels)
	selectors := compileSelectors(n.stack, true, "")

	for _, selector := range selectors {
		n.selMap[selector] = append(n.selMap[selector], group)
	}

	n.extend(group, selectors)
	n.stack = n.stack[:len(n.stack)-1]
	return group
}

func (n *Normalizer) visitMedia(media *MediaNode) Node {
	var medias []*MediaNode
	group := n.closestGroup(media.Block)

	var mergeQueries func(block *BlockNode)
	mergeQueries = func(block *BlockNode) {
		for i, node := range block.Nodes {
			switch v := node.(type) {
			case *MediaNode:
				v.Val = media.Val.(*QueryListNode).Merge(v.Val.(*QueryListNode))
				medias = append(medias, v)
				block.Nodes[i] = NullNode_
			case *BlockNode:
				mergeQueries(v)
			default:
				if b := blockOf(node); b != nil && len(b.Nodes) > 0 {
					mergeQueries(b)
				}
			}
		}
	}
	mergeQueries(media.Block)
	n.bubble(&media.AtruleNode)

	for _, node := range medias {
		if group != nil {
			group.GetBlock().Push(node)
		} else if n.srcRoot != nil {
			n.rootIndex++
			idx := n.rootIndex
			if idx > len(n.srcRoot.Nodes) {
				idx = len(n.srcRoot.Nodes)
			}
			rest := append([]Node{}, n.srcRoot.Nodes[idx:]...)
			n.srcRoot.Nodes = append(append(append([]Node{}, n.srcRoot.Nodes[:idx]...), node), rest...)
		}
		visited := n.visit(node).(*MediaNode)
		parent := visited.Block.Parent
		if visited.Bubbled && (group == nil || (parent != nil && parent.NodeOf != nil &&
			parent.NodeOf.NodeName() == "group")) {
			if visited.Group != nil && len(visited.Block.Nodes) > 0 {
				visited.Group.SetBlock(blockOf(visited.Block.Nodes[0]))
				visited.Block.Nodes[0] = visited.Group
			}
		}
	}
	return media
}

func (n *Normalizer) visitKeyframes(node *KeyframesNode) Node {
	frames := 0
	for _, frame := range node.Block.Nodes {
		if b := blockOf(frame); b != nil && b.HasProperties() {
			frames++
		}
	}
	node.Frames = frames
	return node
}

func (n *Normalizer) extend(group *GroupNode, selectors []string) {
	parent := n.closestGroup(group.GetBlock())
	for _, ext := range group.Extends {
		groups := n.selMap[ext.Selector]
		if groups == nil {
			if ext.Optional {
				continue
			}
			groups = n.checkForPrefixedGroups(ext.Selector)
			if groups == nil {
				err := &JSError{Name: "Error", Message: "Failed to @extend \"" + ext.Selector + "\"",
					Lineno: ext.Lineno, Column: ext.Column, HasPosition: true}
				panic(err)
			}
		}
		for _, selector := range selectors {
			node := NewSelector(nil)
			node.Val = selector
			node.Inherits = false
			for _, g := range groups {
				if parent == nil || parent != g {
					n.extend(g, selectors)
				}
				g.Push(node)
			}
		}
	}
	group.SetBlock(n.visit(group.GetBlock()).(*BlockNode))
}

func (n *Normalizer) checkForPrefixedGroups(selector string) []*GroupNode {
	var prefix []string
	for i := 0; i < len(n.stack); i++ {
		if len(n.stack[i]) == 0 {
			continue
		}
		prefix = append(prefix, n.stack[i][0].Val)
		full := strings.Join(prefix, " ") + " " + selector
		if result := n.selMap[full]; result != nil {
			return result
		}
	}
	return nil
}
