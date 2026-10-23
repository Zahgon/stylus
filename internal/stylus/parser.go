/*!
 * Stylus - Parser
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

var selectorTokens = map[string]bool{
	"ident": true, "string": true, "selector": true, "function": true,
	"comment": true, "boolean": true, "space": true, "color": true,
	"unit": true, "for": true, "in": true, "[": true, "]": true, "(": true,
	")": true, "+": true, "-": true, "*": true, "*=": true, "<": true,
	">": true, "=": true, ":": true, "&": true, "&&": true, "~": true,
	"{": true, "}": true, ".": true, "..": true, "/": true,
}

var pseudoSelectors = map[string]bool{
	"is": true, "has": true, "where": true, "not": true, "dir": true,
	"lang": true, "any-link": true, "link": true, "visited": true,
	"local-link": true, "target": true, "scope": true, "hover": true,
	"active": true, "focus": true, "drop": true, "current": true,
	"past": true, "future": true, "enabled": true, "disabled": true,
	"read-only": true, "read-write": true, "placeholder-shown": true,
	"checked": true, "indeterminate": true, "valid": true, "invalid": true,
	"in-range": true, "out-of-range": true, "required": true,
	"optional": true, "user-error": true, "root": true, "empty": true,
	"blank": true, "nth-child": true, "nth-last-child": true,
	"first-child": true, "last-child": true, "only-child": true,
	"nth-of-type": true, "nth-last-of-type": true, "first-of-type": true,
	"last-of-type": true, "only-of-type": true, "nth-match": true,
	"nth-last-match": true, "nth-column": true, "nth-last-column": true,
	"first-line": true, "first-letter": true, "before": true, "after": true,
	"selection": true,
}

// cssUnits is lib/units.js.
var cssUnits = map[string]bool{
	"em": true, "ex": true, "ch": true, "rem": true,
	"vw": true, "svw": true, "lvw": true, "dvw": true,
	"vh": true, "svh": true, "lvh": true, "dvh": true,
	"vi": true, "svi": true, "lvi": true, "dvi": true,
	"vb": true, "svb": true, "lvb": true, "dvb": true,
	"vmin": true, "svmin": true, "lvmin": true, "dvmin": true,
	"vmax": true, "svmax": true, "lvmax": true, "dvmax": true,
	"cm": true, "mm": true, "in": true, "pt": true, "pc": true, "px": true,
	"deg": true, "grad": true, "rad": true, "turn": true,
	"s": true, "ms": true, "Hz": true, "kHz": true,
	"dpi": true, "dpcm": true, "dppx": true, "x": true,
	"%": true, "fr": true,
}

// memoryCache is lib/cache/memory.js; the original kept one on the Parser
// class, shared by every parse in the process.
type memoryCache struct {
	limit   int
	entries map[string]Node
	order   []string
	enabled bool
}

var parserCache *memoryCache

func getCache(opts ParserOptions) *memoryCache {
	if opts.NoCache {
		return &memoryCache{enabled: false}
	}
	limit := opts.CacheLimit
	if limit == 0 {
		limit = 256
	}
	return &memoryCache{limit: limit, entries: map[string]Node{}, enabled: true}
}

func (c *memoryCache) key(str string, opts ParserOptions) string {
	if !c.enabled {
		return ""
	}
	h := sha1.Sum([]byte(str + jsUndefinedOr(opts.Prefix)))
	return hex.EncodeToString(h[:])
}

// jsUndefinedOr is JavaScript's `str + options.prefix` when prefix is unset.
func jsUndefinedOr(s string) string {
	if s == "" {
		return "undefined"
	}
	return s
}

func (c *memoryCache) has(key string) bool {
	if !c.enabled {
		return false
	}
	_, ok := c.entries[key]
	return ok
}

func (c *memoryCache) get(key string) Node {
	v := c.entries[key]
	// Move to the back, as the original's linked list did.
	for i, k := range c.order {
		if k == key {
			c.order = append(append(append([]string{}, c.order[:i]...), c.order[i+1:]...), key)
			break
		}
	}
	return v.CloneNode(nil, nil)
}

func (c *memoryCache) set(key string, value Node) {
	if !c.enabled {
		return
	}
	clone := value.CloneNode(nil, nil)
	b := clone.base()
	b.Filename = curFilename
	b.Lineno = curLineno
	b.Column = curColumn
	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
	}
	c.entries[key] = clone
	if len(c.order) > c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

// ParserOptions are the options the renderer passes down.
type ParserOptions struct {
	Prefix string
	// Root is the node the statements are pushed into: a Root for a
	// stylesheet, a Block for an imported file.
	Root       Node
	NoCache    bool
	CacheLimit int
}

// Parser turns tokens into a parse tree.
type Parser struct {
	lexer         *Lexer
	hash          string
	cached        bool
	prefix        string
	root          Node
	rootBlock     *BlockNode
	state         []string
	prevState     string
	stash         []*Token
	parens        int
	css           int
	parent        *BlockNode
	bracketed     bool
	cond          bool
	inProperty    bool
	allowPostfix  bool
	operand       bool
	selectorScope string
	identTok      *Token
}

// NewParser prepares a parse of `str`.
func NewParser(str string, options ParserOptions) *Parser {
	if parserCache == nil {
		parserCache = getCache(options)
	}
	p := &Parser{
		prefix: options.Prefix,
		root:   options.Root,
		state:  []string{"root"},
	}
	if p.root == nil {
		p.root = NewRoot()
	}
	switch r := p.root.(type) {
	case *RootNode:
		p.rootBlock = &r.BlockNode
	case *BlockNode:
		p.rootBlock = r
	}
	p.hash = parserCache.key(str, options)
	if !parserCache.has(p.hash) {
		p.lexer = NewLexer(str)
	} else {
		p.cached = true
	}
	return p
}

func (p *Parser) pushState(s string) { p.state = append(p.state, s) }

func (p *Parser) popState() {
	p.prevState = p.state[len(p.state)-1]
	p.state = p.state[:len(p.state)-1]
}

func (p *Parser) currentState() string { return p.state[len(p.state)-1] }

func (p *Parser) previousState() string {
	if len(p.state) < 2 {
		return ""
	}
	return p.state[len(p.state)-2]
}

// Parse parses the whole stylesheet.
func (p *Parser) Parse() Node {
	if p.cached {
		return parserCache.get(p.hash)
	}
	block := p.rootBlock
	p.parent = p.rootBlock
	for p.peek().Type != "eos" {
		p.skipWhitespace()
		if p.peek().Type == "eos" {
			break
		}
		stmt := p.statement()
		p.accept(";")
		if stmt == nil {
			p.error("unexpected token {peek}, not allowed at the root level")
		}
		block.Push(stmt)
	}
	parserCache.set(p.hash, p.root)
	return p.root
}

func (p *Parser) error(msg string) {
	tok := p.peek()
	typ := tok.Type
	val := ""
	if tok.Val != nil || tok.HasStrVal {
		val = " " + tok.String()
	}
	if strings.TrimSpace(val) == strings.TrimSpace(typ) {
		val = ""
	}
	throwNamed("ParseError", "%s", strings.Replace(msg, "{peek}", "\""+typ+val+"\"", 1))
}

func (p *Parser) accept(typ string) *Token {
	if typ == p.peek().Type {
		return p.next()
	}
	return nil
}

func (p *Parser) expect(typ string) *Token {
	if typ != p.peek().Type {
		p.error("expected \"" + typ + "\", got {peek}")
	}
	return p.next()
}

func (p *Parser) next() *Token {
	var tok *Token
	if n := len(p.stash); n > 0 {
		tok = p.stash[n-1]
		p.stash = p.stash[:n-1]
	} else {
		tok = p.lexer.Next()
	}
	line := tok.Lineno
	column := tok.Column
	if column == 0 {
		column = 1
	}
	if tok.Val != nil {
		tok.Val.base().Lineno = line
		tok.Val.base().Column = column
	}
	curLineno = line
	curColumn = column
	return tok
}

func (p *Parser) peek() *Token { return p.lexer.Peek() }

func (p *Parser) lookahead(n int) *Token {
	if n <= 0 {
		return &Token{Type: "\x00none"}
	}
	return p.lexer.Lookahead(n)
}

func (p *Parser) isSelectorToken(n int) bool {
	la := p.lookahead(n).Type
	switch la {
	case "for":
		return p.bracketed
	case "[":
		p.bracketed = true
		return true
	case "]":
		p.bracketed = false
		return true
	}
	return selectorTokens[la]
}

func (p *Parser) isPseudoSelector(n int) bool {
	val := p.lookahead(n).Val
	if id, ok := val.(*IdentNode); ok {
		return pseudoSelectors[id.Name]
	}
	return false
}

func (p *Parser) lineContains(typ string) bool {
	for i := 1; ; i++ {
		la := p.lookahead(i)
		switch la.Type {
		case "indent", "outdent", "newline", "eos":
			return false
		}
		if typ == la.Type {
			return true
		}
	}
}

func (p *Parser) selectorToken() *Token {
	if p.isSelectorToken(1) {
		if p.peek().Type == "{" {
			if !p.lineContains("}") {
				return nil
			}
			for i := 1; ; i++ {
				la := p.lookahead(i)
				if la.Type == "}" {
					if i == 2 || (i == 3 && p.lookahead(i-1).Type == "space") {
						return nil
					}
					break
				}
				if la.Type == ":" {
					return nil
				}
				if la.Type == "eos" {
					break
				}
			}
		}
		return p.next()
	}
	return nil
}

func (p *Parser) skip(types ...string) {
	for {
		t := p.peek().Type
		found := false
		for _, x := range types {
			if x == t {
				found = true
				break
			}
		}
		if !found {
			return
		}
		p.next()
	}
}

func (p *Parser) skipWhitespace() { p.skip("space", "indent", "outdent", "newline") }
func (p *Parser) skipNewlines()   { p.skip("newline") }
func (p *Parser) skipSpaces()     { p.skip("space") }
func (p *Parser) skipSpacesAndComments() {
	for p.peek().Type == "space" || p.peek().Type == "comment" {
		p.next()
	}
}

func (p *Parser) looksLikeFunctionDefinition(i int) bool {
	return p.lookahead(i).Type == "indent" || p.lookahead(i).Type == "{"
}

func indexOfType(list []string, t string) bool {
	for _, x := range list {
		if x == t {
			return true
		}
	}
	return false
}

func (p *Parser) looksLikeSelector(fromProperty bool) bool {
	i := 1
	brace := false

	// Real property
	if fromProperty && p.lookahead(i+1).Type == ":" &&
		(p.lookahead(i+1).Space != "" || p.lookahead(i+2).Type == "indent") {
		return false
	}

	for p.lookahead(i).Type == "ident" &&
		(p.lookahead(i+1).Type == "newline" || p.lookahead(i+1).Type == ",") {
		i += 2
	}

	for p.isSelectorToken(i) || p.lookahead(i).Type == "," {
		if p.lookahead(i).Type == "selector" {
			return true
		}
		if p.lookahead(i+1).Type == "&" {
			return true
		}
		if i > 1 && p.lookahead(i-1).Type == "ident" && p.lookahead(i).Type == "." &&
			p.lookahead(i+1).Type == "ident" {
			for {
				node := p.lookahead(i + 2)
				if indexOfType([]string{"indent", "outdent", "{", ";", "eos", "selector",
					"media", "if", "atrule", ")", "}", "unit", "[", "for", "function"}, node.Type) {
					if node.Type == "[" {
						for {
							node = p.lookahead(i + 3)
							if node.Type == "]" {
								break
							}
							if node.Type == "." || node.Type == "unit" {
								return false
							}
							i++
							if node.Type == "eos" {
								break
							}
						}
					} else {
						if p.isPseudoSelector(i + 2) {
							return true
						}
						if node.Type == ")" && p.lookahead(i+3).Type == "}" {
							break
						}
						return !indexOfType([]string{"outdent", ";", "eos", "media", "if",
							"atrule", ")", "}", "unit", "for", "function"}, node.Type)
					}
				}
				i++
			}
			return true
		}
		if p.lookahead(i).Type == "." && p.lookahead(i+1).Type == "ident" {
			return true
		}
		if p.lookahead(i).Type == "*" && p.lookahead(i+1).Type == "newline" {
			return true
		}
		// Pseudo-elements
		if p.lookahead(i).Type == ":" && p.lookahead(i+1).Type == ":" {
			return true
		}
		// #a after an ident and newline
		if p.lookahead(i).Type == "color" && p.lookahead(i-1).Type == "newline" {
			return true
		}
		if p.looksLikeAttributeSelector(i) {
			return true
		}
		if (p.lookahead(i).Type == "=" || p.lookahead(i).Type == "function") &&
			p.lookahead(i+1).Type == "{" {
			return false
		}
		// Hash values inside properties
		if p.lookahead(i).Type == ":" && !p.isPseudoSelector(i+1) && p.lineContains(".") {
			return false
		}
		if p.lookahead(i).Type == "{" {
			brace = true
		} else if p.lookahead(i).Type == "}" {
			brace = false
		}
		if brace && p.lookahead(i).Type == ":" {
			return true
		}
		if p.lookahead(i).Type == "space" && p.lookahead(i+1).Type == "{" {
			return true
		}
		// Assume pseudo selectors are NOT properties
		colon := p.lookahead(i).Type == ":"
		i++
		if colon && p.lookahead(i-1).Space == "" && p.isPseudoSelector(i) {
			return true
		}
		// Trailing space
		if p.lookahead(i).Type == "space" && p.lookahead(i+1).Type == "newline" &&
			p.lookahead(i+2).Type == "{" {
			return true
		}
		if p.lookahead(i).Type == "," && p.lookahead(i+1).Type == "newline" {
			return true
		}
	}

	// Trailing comma
	if p.lookahead(i).Type == "," && p.lookahead(i+1).Type == "newline" {
		return true
	}
	// Trailing brace
	if p.lookahead(i).Type == "{" && p.lookahead(i+1).Type == "newline" {
		return true
	}
	// css-style mode, false on ; }
	if p.css > 0 {
		if p.lookahead(i).Type == ";" || p.lookahead(i-1).Type == "}" {
			return false
		}
	}
	// Trailing separators
	for !indexOfType([]string{"indent", "outdent", "newline", "for", "if", ";", "}", "eos"},
		p.lookahead(i).Type) {
		i++
	}
	return p.lookahead(i).Type == "indent"
}

func (p *Parser) looksLikeAttributeSelector(n int) bool {
	typ := p.lookahead(n).Type
	if typ == "=" && p.bracketed {
		return true
	}
	return (typ == "ident" || typ == "string") &&
		p.lookahead(n+1).Type == "]" &&
		(p.lookahead(n+2).Type == "newline" || p.isSelectorToken(n+2)) &&
		!p.lineContains(":") && !p.lineContains("=")
}

func (p *Parser) looksLikeKeyframe() bool {
	i := 2
	switch p.lookahead(i).Type {
	case "{", "indent", ",":
		return true
	case "newline":
		for {
			i++
			t := p.lookahead(i).Type
			if t != "unit" && t != "newline" {
				break
			}
		}
		typ := p.lookahead(i).Type
		return typ == "indent" || typ == "{"
	}
	return false
}

func (p *Parser) stateAllowsSelector() bool {
	switch p.currentState() {
	case "root", "atblock", "selector", "conditional", "function", "atrule", "for":
		return true
	}
	return false
}

func (p *Parser) assignAtblock(expr *ExpressionNode) {
	err := catch(func() {
		expr.Push(p.atblock(true))
	})
	if err != nil {
		p.error("invalid right-hand side operand in assignment, got {peek}")
	}
}

func (p *Parser) statement() Node {
	stmt := p.stmt()
	state := p.prevState

	if p.allowPostfix {
		p.allowPostfix = false
		state = "expression"
	}

	switch state {
	case "assignment", "expression", "function arguments":
		for {
			var op *Token
			if op = p.accept("if"); op == nil {
				if op = p.accept("unless"); op == nil {
					op = p.accept("for")
				}
			}
			if op == nil {
				break
			}
			switch op.Type {
			case "if", "unless":
				ifNode := NewIf(p.expression(), false)
				ifNode.Block = stmt
				ifNode.Postfix = true
				ifNode.Negate = op.Type == "unless"
				stmt = ifNode
				p.accept(";")
			case "for":
				key := ""
				val := idName(p.id())
				if p.accept(",") != nil {
					key = idName(p.id())
				}
				p.expect("in")
				each := NewEach(val, key, p.expression(), nil)
				block := NewBlock(p.parent, each)
				block.Push(stmt)
				each.Block = block
				stmt = each
			}
		}
	}
	return stmt
}

func (p *Parser) stmt() Node {
	tok := p.peek()
	switch tok.Type {
	case "keyframes":
		return p.keyframes()
	case "-moz-document":
		return p.mozdocument()
	case "comment":
		return p.comment()
	case "selector":
		return p.selector()
	case "literal":
		return p.literal()
	case "charset":
		return p.charset()
	case "namespace":
		return p.namespace()
	case "import":
		return p.importRule()
	case "require":
		return p.requireRule()
	case "extend":
		return p.extend()
	case "media":
		return p.media()
	case "atrule":
		return p.atrule()
	case "ident":
		return p.ident()
	case "scope":
		return p.scope()
	case "supports":
		return p.supports()
	case "unless":
		return p.unless()
	case "function":
		return p.functionStmt()
	case "for":
		return p.forStmt()
	case "if":
		return p.ifStmt()
	case "return":
		return p.returnStmt()
	case "{":
		return p.property()
	}

	if p.stateAllowsSelector() {
		switch tok.Type {
		case "color", "~", ">", "<", ":", "&", "&&", "[", ".", "/":
			sel := p.selector()
			sel.base().Column = tok.Column
			sel.base().Lineno = tok.Lineno
			return sel
		case "..":
			if p.lookahead(2).Type == "/" {
				return p.selector()
			}
			fallthrough
		case "+":
			if p.lookahead(2).Type == "function" {
				return p.functionCall()
			}
			return p.selector()
		case "*":
			return p.property()
		case "unit":
			if p.looksLikeKeyframe() {
				sel := p.selector()
				sel.base().Column = tok.Column
				sel.base().Lineno = tok.Lineno
				return sel
			}
			if p.lookahead(2).Type == "{" {
				return p.property()
			}
		case "-":
			if p.lookahead(2).Type == "{" {
				return p.property()
			}
		}
	}

	expr := p.expression()
	if expr.IsEmpty() {
		p.error("unexpected {peek}")
	}
	return expr
}

func (p *Parser) block(node Node, scope bool) *BlockNode {
	block := NewBlock(p.parent, node)
	p.parent = block
	if !scope {
		block.Scope = false
	}
	p.accept("newline")

	delim := ""
	if p.accept("{") != nil {
		p.css++
		delim = "}"
		p.skipWhitespace()
	} else {
		delim = "outdent"
		p.expect("indent")
	}

	for delim != p.peek().Type {
		var stmt Node
		if p.css > 0 {
			if p.accept("newline") != nil || p.accept("indent") != nil {
				continue
			}
			stmt = p.statement()
			p.accept(";")
			p.skipWhitespace()
		} else {
			if p.accept("newline") != nil {
				continue
			}
			next := p.lookahead(2).Type
			if p.peek().Type == "indent" &&
				(next == "outdent" || next == "newline" || next == "comment") {
				p.skip("indent", "outdent")
				continue
			}
			if p.peek().Type == "eos" {
				return block
			}
			stmt = p.statement()
			p.accept(";")
		}
		if stmt == nil {
			p.error("unexpected token {peek} in block")
		}
		block.Push(stmt)
	}

	if p.css > 0 {
		p.skipWhitespace()
		p.expect("}")
		p.skipSpaces()
		p.css--
	} else {
		p.expect("outdent")
	}
	p.parent = block.Parent
	return block
}

func (p *Parser) comment() Node {
	node := p.next().Val
	p.skipSpaces()
	return node
}

func (p *Parser) forStmt() Node {
	p.expect("for")
	key := ""
	val := idName(p.id())
	if p.accept(",") != nil {
		key = idName(p.id())
	}
	p.expect("in")
	p.pushState("for")
	p.cond = true
	each := NewEach(val, key, p.expression(), nil)
	p.cond = false
	each.Block = p.block(each, false)
	p.popState()
	return each
}

func (p *Parser) returnStmt() Node {
	p.expect("return")
	expr := p.expression()
	if expr.IsEmpty() {
		return NewReturn(nil)
	}
	return NewReturn(expr)
}

func (p *Parser) unless() Node {
	p.expect("unless")
	p.pushState("conditional")
	p.cond = true
	node := NewIf(p.expression(), true)
	p.cond = false
	node.Block = p.block(node, false)
	p.popState()
	return node
}

func (p *Parser) ifStmt() Node {
	token := p.expect("if")
	p.pushState("conditional")
	p.cond = true
	node := NewIf(p.expression(), false)
	node.Column = token.Column
	p.cond = false
	node.Block = p.block(node, false)
	p.skip("newline", "comment")
	for p.accept("else") != nil {
		tok := p.accept("if")
		if tok != nil {
			p.cond = true
			cond := p.expression()
			p.cond = false
			block := p.block(node, false)
			item := NewIf(cond, false)
			item.Block = block
			item.Column = tok.Column
			node.Elses = append(node.Elses, item)
		} else {
			node.Elses = append(node.Elses, p.block(node, false))
			break
		}
		p.skip("newline", "comment")
	}
	p.popState()
	return node
}

func (p *Parser) atblock(fromAssignment bool) Node {
	if !fromAssignment {
		p.expect("atblock")
	}
	node := NewAtblock()
	p.pushState("atblock")
	node.Block = p.block(node, false)
	p.popState()
	return node
}

func (p *Parser) atrule() Node {
	typ := p.expect("atrule").StrVal
	node := NewAtrule(typ)
	p.skipSpacesAndComments()
	node.Segments = p.selectorParts()
	p.skipSpacesAndComments()
	tok := p.peek().Type
	if tok == "indent" || tok == "{" || (tok == "newline" && p.lookahead(2).Type == "{") {
		p.pushState("atrule")
		node.Block = p.block(node, true)
		p.popState()
	}
	return node
}

func (p *Parser) scope() Node {
	p.expect("scope")
	var b strings.Builder
	for _, s := range p.selectorParts() {
		if lit, ok := s.(*LiteralNode); ok {
			b.WriteString(lit.Val)
		} else {
			b.WriteString(s.String())
		}
	}
	p.selectorScope = strings.TrimSpace(b.String())
	return NullNode_
}

func (p *Parser) supports() Node {
	p.expect("supports")
	node := NewSupports(p.supportsCondition())
	p.pushState("atrule")
	node.Block = p.block(node, true)
	p.popState()
	return node
}

func (p *Parser) supportsCondition() Node {
	node := p.supportsNegation()
	if node == nil {
		node = p.supportsOp()
	}
	if node == nil {
		p.cond = true
		node = p.expression()
		p.cond = false
	}
	return node
}

func (p *Parser) supportsNegation() Node {
	if p.accept("not") != nil {
		node := NewExpression(false)
		node.Push(NewLiteral("not"))
		node.Push(p.supportsFeature())
		return node
	}
	return nil
}

func (p *Parser) supportsOp() Node {
	feature := p.supportsFeature()
	if feature == nil {
		return nil
	}
	expr := NewExpression(false)
	expr.Push(feature)
	for {
		op := p.accept("&&")
		if op == nil {
			op = p.accept("||")
		}
		if op == nil {
			break
		}
		if op.StrVal == "&&" {
			expr.Push(NewLiteral("and"))
		} else {
			expr.Push(NewLiteral("or"))
		}
		expr.Push(p.supportsFeature())
	}
	return expr
}

func (p *Parser) supportsFeature() Node {
	p.skipSpacesAndComments()
	if p.peek().Type == "(" {
		la := p.lookahead(2).Type
		if la == "ident" || la == "{" {
			return p.feature()
		}
		p.expect("(")
		node := NewExpression(false)
		node.Push(NewLiteral("("))
		node.Push(p.supportsCondition())
		p.expect(")")
		node.Push(NewLiteral(")"))
		p.skipSpacesAndComments()
		return node
	}
	return nil
}

func (p *Parser) extend() Node {
	tok := p.expect("extend")
	var extendSelectors []*SelectorNode
	for {
		arr := p.selectorParts()
		if len(arr) > 0 {
			sel := NewSelector(arr)
			optional := false
			if p.peek().Type == "!" {
				t2 := p.lookahead(2)
				if id, ok := t2.Val.(*IdentNode); ok && t2.Type == "ident" && id.Name == "optional" {
					p.skip("!", "ident")
					optional = true
				}
			}
			sel.Optional = optional
			extendSelectors = append(extendSelectors, sel)
		}
		if p.accept(",") == nil {
			break
		}
	}
	node := NewExtendSelectors(extendSelectors)
	node.Lineno = tok.Lineno
	node.Column = tok.Column
	return node
}

func (p *Parser) media() Node {
	p.expect("media")
	p.pushState("atrule")
	media := NewMedia(p.queries())
	media.Block = p.block(media, true)
	p.popState()
	return media
}

func (p *Parser) queries() *QueryListNode {
	queries := NewQueryList()
	for {
		p.skip("comment", "newline", "space")
		queries.Push(p.query())
		p.skip("comment", "newline", "space")
		if p.accept(",") == nil {
			break
		}
	}
	return queries
}

func (p *Parser) query() Node {
	query := NewQuery()

	if p.peek().Type == "ident" &&
		(p.lookahead(2).Type == "." || p.lookahead(2).Type == "[") {
		p.cond = true
		expr := p.expression()
		p.cond = false
		query.Push(NewFeature(expr.Nodes))
		return query
	}

	predTok := p.accept("ident")
	if predTok == nil {
		predTok = p.accept("not")
	}
	if predTok != nil {
		var predStr string
		if predTok.Val != nil {
			predStr = stringOf(predTok.Val)
			if predStr == "" {
				predStr = predTok.Val.String()
			}
		} else {
			predStr = predTok.StrVal
		}
		pred := NewLiteral(predStr)
		p.skipSpacesAndComments()
		if id := p.accept("ident"); id != nil {
			query.Type = id.Val
			query.Predicate = pred
		} else {
			query.Type = pred
		}
		p.skipSpacesAndComments()
		if p.accept("&&") == nil {
			return query
		}
	}

	for {
		query.Push(p.feature())
		if p.accept("&&") == nil {
			break
		}
	}
	return query
}

func (p *Parser) feature() Node {
	p.skipSpacesAndComments()
	p.expect("(")
	p.skipSpacesAndComments()
	node := NewFeature(p.interpolate())
	p.skipSpacesAndComments()
	p.accept(":")
	p.skipSpacesAndComments()
	p.inProperty = true
	node.Expr = p.list()
	p.inProperty = false
	p.skipSpacesAndComments()
	p.expect(")")
	p.skipSpacesAndComments()
	return node
}

func (p *Parser) mozdocument() Node {
	p.expect("-moz-document")
	mozdocument := NewAtrule("-moz-document")
	var calls []string
	for {
		p.skipSpacesAndComments()
		calls = append(calls, p.functionCall().String())
		p.skipSpacesAndComments()
		if p.accept(",") == nil {
			break
		}
	}
	mozdocument.Segments = []Node{NewLiteral(strings.Join(calls, ", "))}
	p.pushState("atrule")
	mozdocument.Block = p.block(mozdocument, false)
	p.popState()
	return mozdocument
}

func (p *Parser) importRule() Node {
	p.expect("import")
	p.allowPostfix = true
	return NewImport(p.expression(), false)
}

func (p *Parser) requireRule() Node {
	p.expect("require")
	p.allowPostfix = true
	return NewImport(p.expression(), true)
}

func (p *Parser) charset() Node {
	p.expect("charset")
	str := p.expect("string").Val
	p.allowPostfix = true
	return NewCharset(str.String())
}

func (p *Parser) namespace() Node {
	p.expect("namespace")
	p.skipSpacesAndComments()
	prefix := ""
	if tok := p.accept("ident"); tok != nil {
		prefix = tok.Val.String()
	}
	p.skipSpacesAndComments()
	var str Node
	if tok := p.accept("string"); tok != nil {
		str = tok.Val
	} else {
		str = p.url()
	}
	p.allowPostfix = true
	return NewNamespace(str, prefix)
}

func (p *Parser) keyframes() Node {
	tok := p.expect("keyframes")
	p.skipSpacesAndComments()
	keyframes := NewKeyframes(p.selectorParts(), tok.StrVal)
	keyframes.Column = tok.Column
	p.skipSpacesAndComments()
	p.pushState("atrule")
	keyframes.Block = p.block(keyframes, true)
	p.popState()
	return keyframes
}

func (p *Parser) literal() Node { return p.expect("literal").Val }

// id returns the value of an ident token; `!important` and escaped characters
// arrive as literals, which the original also returned unchanged.
func (p *Parser) id() Node {
	tok := p.expect("ident")
	p.accept("space")
	return tok.Val
}

// idName is the `.name` property of what id() returns.
func idName(n Node) string {
	if id, ok := n.(*IdentNode); ok {
		return id.Name
	}
	return ""
}

func (p *Parser) ident() Node {
	i := 2
	la := p.lookahead(i).Type
	for la == "space" {
		i++
		la = p.lookahead(i).Type
	}
	fellThrough := false

	switch la {
	case "=", "?=", "-=", "+=", "*=", "/=", "%=":
		return p.assignment()
	}

	if la == "." {
		if p.lookahead(i-1).Type == "space" {
			return p.selector()
		}
		if p.identTok == p.peek() {
			return p.id()
		}
		for {
			i++
			t := p.lookahead(i).Type
			if t == "=" || indexOfType([]string{"[", ",", "newline", "indent", "eos"}, t) {
				break
			}
		}
		if p.lookahead(i).Type == "=" {
			p.identTok = p.peek()
			return p.expression()
		} else if p.looksLikeSelector(false) && p.stateAllowsSelector() {
			return p.selector()
		}
		fellThrough = true
	}

	if la == "[" || fellThrough {
		if p.identTok == p.peek() {
			return p.id()
		}
		for {
			t := p.lookahead(i).Type
			i++
			if t == "]" {
				break
			}
			if p.lookahead(i).Type == "selector" || p.lookahead(i).Type == "eos" {
				break
			}
		}
		if p.lookahead(i).Type == "=" {
			p.identTok = p.peek()
			return p.expression()
		} else if p.looksLikeSelector(false) && p.stateAllowsSelector() {
			return p.selector()
		}
		// falls through to the operation case
		return p.identOperation(la)
	}

	switch la {
	case "-", "+", "/", "*", "%", "**", "&&", "||", ">", "<", ">=", "<=",
		"!=", "==", "?", "in", "is a", "is defined":
		return p.identOperation(la)
	}

	switch p.currentState() {
	case "root":
		return p.selector()
	case "for", "selector", "function", "conditional", "atblock", "atrule":
		return p.property()
	}
	id := p.id()
	if p.previousState() == "interpolation" {
		if n, ok := id.(*IdentNode); ok {
			n.Mixin = true
		}
	}
	return id
}

func (p *Parser) identOperation(la string) Node {
	if p.identTok == p.peek() {
		return p.id()
	}
	p.identTok = p.peek()
	switch p.currentState() {
	case "for", "selector":
		return p.property()
	case "root", "atblock", "atrule":
		if la == "[" {
			return p.subscript()
		}
		return p.selector()
	case "function", "conditional":
		if p.looksLikeSelector(false) {
			return p.selector()
		}
		return p.expression()
	default:
		if p.operand {
			return p.id()
		}
		return p.expression()
	}
}

func (p *Parser) interpolate() []Node {
	var segs []Node
	if p.accept("*") != nil {
		segs = append(segs, NewLiteral("*"))
	}
	for {
		if p.accept("{") != nil {
			p.pushState("interpolation")
			segs = append(segs, p.expression())
			p.expect("}")
			p.popState()
		} else if p.accept("-") != nil {
			segs = append(segs, NewLiteral("-"))
		} else if node := p.accept("ident"); node != nil {
			segs = append(segs, node.Val)
		} else {
			break
		}
	}
	if len(segs) == 0 {
		p.expect("ident")
	}
	return segs
}

func (p *Parser) property() Node {
	if p.looksLikeSelector(true) {
		return p.selector()
	}
	ident := p.interpolate()
	prop := NewProperty(ident, nil)
	var ret Node = prop

	p.accept("space")
	if p.accept(":") != nil {
		p.accept("space")
	}
	p.pushState("property")
	p.inProperty = true
	prop.Expr = p.list()
	if e, ok := prop.Expr.(*ExpressionNode); ok && e.IsEmpty() {
		ret = ident[0]
	}
	p.inProperty = false
	p.allowPostfix = true
	p.popState()
	p.accept(";")
	return ret
}

func (p *Parser) selector() Node {
	group := NewGroup()
	scope := p.selectorScope
	isRoot := p.currentState() == "root"
	for {
		p.accept("newline")
		arr := p.selectorParts()
		if isRoot && scope != "" {
			arr = append([]Node{NewLiteral(scope + " ")}, arr...)
		}
		if len(arr) > 0 {
			selector := NewSelector(arr)
			selector.Lineno = arr[0].base().Lineno
			selector.Column = arr[0].base().Column
			group.Push(selector)
		}
		if p.accept(",") == nil && p.accept("newline") == nil {
			break
		}
	}
	if p.currentState() == "selector-parts" {
		list := NewExpression(false)
		list.Nodes = group.Nodes
		return list
	}
	p.pushState("selector")
	group.SetBlock(p.block(group, true))
	p.popState()
	return group
}

func (p *Parser) selectorParts() []Node {
	var arr []Node
	for {
		tok := p.selectorToken()
		if tok == nil {
			break
		}
		switch {
		case tok.Type == "{":
			p.skipSpaces()
			expr := p.expression()
			p.skipSpaces()
			p.expect("}")
			arr = append(arr, expr)
		case p.prefix != "" && tok.Type == ".":
			literal := NewLiteral(tok.StrVal + p.prefix)
			literal.Prefixed = true
			arr = append(arr, literal)
		case tok.Type == "comment":
			// ignore comments
		case tok.Type == "color" || tok.Type == "unit":
			raw := ""
			switch v := tok.Val.(type) {
			case *RGBANode:
				raw = v.Raw
			case *UnitNode:
				raw = v.Raw
			}
			arr = append(arr, NewLiteral(raw))
		case tok.Type == "space":
			arr = append(arr, NewLiteral(" "))
		case tok.Type == "function":
			arr = append(arr, NewLiteral(tok.Val.(*IdentNode).Name+"("))
		case tok.Type == "ident":
			switch v := tok.Val.(type) {
			case *IdentNode:
				arr = append(arr, NewLiteral(v.Name))
			case *LiteralNode:
				arr = append(arr, NewLiteral(v.Str))
			default:
				arr = append(arr, NewLiteral(tok.Val.String()))
			}
		default:
			if tok.HasStrVal {
				arr = append(arr, NewLiteral(tok.StrVal))
			} else if tok.Val != nil {
				arr = append(arr, NewLiteral(tok.Val.String()))
			} else {
				arr = append(arr, NewLiteral("undefined"))
			}
			if tok.Space != "" {
				arr = append(arr, NewLiteral(" "))
			}
		}
	}
	return arr
}

func (p *Parser) assignment() Node {
	ident := p.id()
	name := idName(ident)
	var op *Token
	for _, t := range []string{"=", "?=", "+=", "-=", "*=", "/=", "%="} {
		if op = p.accept(t); op != nil {
			break
		}
	}
	if op == nil {
		return nil
	}
	p.pushState("assignment")
	expr := p.list()
	if e, ok := expr.(*ExpressionNode); ok && e.IsEmpty() {
		p.assignAtblock(e)
	}
	node := NewIdent(name, expr, false)
	node.Lineno = ident.base().Lineno
	node.Column = ident.base().Column
	p.popState()
	var ret Node = node
	switch op.Type {
	case "?=":
		defined := NewBinOp("is defined", node, nil)
		lookup := NewExpression(false)
		lookup.Push(NewIdent(name, nil, false))
		ret = NewTernary(defined, lookup, node)
	case "+=", "-=", "*=", "/=", "%=":
		node.Val = NewBinOp(string(op.Type[0]), NewIdent(name, nil, false), expr)
	}
	return ret
}

func (p *Parser) functionStmt() Node {
	parens := 1
	i := 2
	for {
		tok := p.lookahead(i)
		i++
		switch tok.Type {
		case "function", "(":
			parens++
		case ")":
			parens--
			if parens == 0 {
				goto done
			}
		case "eos":
			p.error("failed to find closing paren \")\"")
		}
	}
done:
	if p.currentState() == "expression" {
		return p.functionCall()
	}
	if p.looksLikeFunctionDefinition(i) {
		return p.functionDefinition()
	}
	return p.expression()
}

func (p *Parser) url() Node {
	p.expect("function")
	p.pushState("function arguments")
	args := p.args()
	p.expect(")")
	p.popState()
	return NewCall("url", args)
}

func (p *Parser) functionCall() Node {
	withBlock := p.accept("+") != nil
	if id, ok := p.peek().Val.(*IdentNode); ok && id.Name == "url" {
		return p.url()
	}
	tok := p.expect("function").Val.(*IdentNode)
	name := tok.Name
	p.pushState("function arguments")
	p.parens++
	args := p.args()
	p.expect(")")
	p.parens--
	p.popState()
	call := NewCall(name, args)
	call.Column = tok.Column
	call.Lineno = tok.Lineno
	if withBlock {
		p.pushState("function")
		call.Block = p.block(call, true)
		p.popState()
	}
	return call
}

func (p *Parser) functionDefinition() Node {
	tok := p.expect("function")
	name := tok.Val.(*IdentNode).Name
	p.pushState("function params")
	p.skipWhitespace()
	params := p.params()
	p.skipWhitespace()
	p.expect(")")
	p.popState()
	p.pushState("function")
	fn := NewFunction(name, params, nil)
	fn.Column = tok.Val.base().Column
	fn.Lineno = tok.Val.base().Lineno
	fn.Block = p.block(fn, true)
	p.popState()
	return NewIdent(name, fn, false)
}

func (p *Parser) params() *ParamsNode {
	params := NewParams()
	for {
		tok := p.accept("ident")
		if tok == nil {
			break
		}
		p.accept("space")
		node := tok.Val.(*IdentNode)
		params.Push(node)
		if p.accept("...") != nil {
			node.Rest = true
		} else if p.accept("=") != nil {
			node.Val = p.expression()
		}
		p.skipWhitespace()
		p.accept(",")
		p.skipWhitespace()
	}
	return params
}

func (p *Parser) args() *ArgumentsNode {
	args := NewArguments()
	for {
		if p.peek().Type == "ident" && p.lookahead(2).Type == ":" {
			keyword := stringOf(p.next().Val)
			p.expect(":")
			args.Map.set(keyword, p.expression())
		} else {
			args.Push(p.expression())
		}
		if p.accept(",") == nil {
			break
		}
	}
	return args
}

func (p *Parser) list() Node {
	node := p.expression()
	var list *ExpressionNode
	var cur Node = node
	for p.accept(",") != nil {
		if e, ok := cur.(*ExpressionNode); ok && e.IsList {
			list.Push(p.expression())
		} else {
			list = NewExpression(true)
			list.Push(cur)
			list.Push(p.expression())
			cur = list
		}
	}
	return cur
}

func (p *Parser) expression() *ExpressionNode {
	expr := NewExpression(false)
	p.pushState("expression")
	for {
		node := p.negation()
		if node == nil {
			break
		}
		expr.Push(node)
	}
	p.popState()
	if len(expr.Nodes) > 0 {
		expr.Lineno = expr.Nodes[0].base().Lineno
		expr.Column = expr.Nodes[0].base().Column
	}
	return expr
}

func (p *Parser) negation() Node {
	if p.accept("not") != nil {
		return NewUnaryOp("!", p.negation())
	}
	return p.ternary()
}

func (p *Parser) ternary() Node {
	node := p.logical()
	if p.accept("?") != nil {
		trueExpr := p.expression()
		p.expect(":")
		falseExpr := p.expression()
		node = NewTernary(node, trueExpr, falseExpr)
	}
	return node
}

func (p *Parser) logical() Node {
	node := p.typecheck()
	for {
		op := p.accept("&&")
		if op == nil {
			op = p.accept("||")
		}
		if op == nil {
			break
		}
		node = NewBinOp(op.Type, node, p.typecheck())
	}
	return node
}

func (p *Parser) typecheck() Node {
	node := p.equality()
	for {
		op := p.accept("is a")
		if op == nil {
			break
		}
		p.operand = true
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\", missing left-hand operand")
		}
		node = NewBinOp(op.Type, node, p.equality())
		p.operand = false
	}
	return node
}

func (p *Parser) equality() Node {
	node := p.inOp()
	for {
		op := p.accept("==")
		if op == nil {
			op = p.accept("!=")
		}
		if op == nil {
			break
		}
		p.operand = true
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\", missing left-hand operand")
		}
		node = NewBinOp(op.Type, node, p.inOp())
		p.operand = false
	}
	return node
}

func (p *Parser) inOp() Node {
	node := p.relational()
	for p.accept("in") != nil {
		p.operand = true
		if node == nil {
			p.error("illegal unary \"in\", missing left-hand operand")
		}
		node = NewBinOp("in", node, p.relational())
		p.operand = false
	}
	return node
}

func (p *Parser) relational() Node {
	node := p.rangeOp()
	for {
		var op *Token
		for _, t := range []string{">=", "<=", "<", ">"} {
			if op = p.accept(t); op != nil {
				break
			}
		}
		if op == nil {
			break
		}
		p.operand = true
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\", missing left-hand operand")
		}
		node = NewBinOp(op.Type, node, p.rangeOp())
		p.operand = false
	}
	return node
}

func (p *Parser) rangeOp() Node {
	node := p.additive()
	op := p.accept("...")
	if op == nil {
		op = p.accept("..")
	}
	if op != nil {
		p.operand = true
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\", missing left-hand operand")
		}
		node = NewBinOp(op.StrVal, node, p.additive())
		p.operand = false
	}
	return node
}

func (p *Parser) additive() Node {
	node := p.multiplicative()
	for {
		op := p.accept("+")
		if op == nil {
			op = p.accept("-")
		}
		if op == nil {
			break
		}
		p.operand = true
		node = NewBinOp(op.Type, node, p.multiplicative())
		p.operand = false
	}
	return node
}

func (p *Parser) multiplicative() Node {
	node := p.defined()
	for {
		var op *Token
		for _, t := range []string{"**", "*", "/", "%"} {
			if op = p.accept(t); op != nil {
				break
			}
		}
		if op == nil {
			break
		}
		p.operand = true
		if op.Type == "/" && p.inProperty && p.parens == 0 {
			p.stash = append(p.stash, newTokenVal("literal", NewLiteral("/")))
			p.operand = false
			return node
		}
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\", missing left-hand operand")
		}
		node = NewBinOp(op.Type, node, p.defined())
		p.operand = false
	}
	return node
}

func (p *Parser) defined() Node {
	node := p.unary()
	if p.accept("is defined") != nil {
		if node == nil {
			p.error("illegal unary \"is defined\", missing left-hand operand")
		}
		node = NewBinOp("is defined", node, nil)
	}
	return node
}

func (p *Parser) unary() Node {
	var op *Token
	for _, t := range []string{"!", "~", "+", "-"} {
		if op = p.accept(t); op != nil {
			break
		}
	}
	if op != nil {
		p.operand = true
		node := p.unary()
		if node == nil {
			p.error("illegal unary \"" + op.String() + "\"")
		}
		res := NewUnaryOp(op.Type, node)
		p.operand = false
		return res
	}
	return p.subscript()
}

func (p *Parser) subscript() Node {
	node := p.member()
	for p.accept("[") != nil {
		node = NewBinOp("[]", node, p.expression())
		p.expect("]")
	}
	if p.accept("=") != nil {
		if b, ok := node.(*BinOpNode); ok {
			b.Op += "="
			val := p.list()
			b.Val = val
			if e, ok := val.(*ExpressionNode); ok && e.IsEmpty() {
				p.assignAtblock(e)
			}
		}
	}
	return node
}

func (p *Parser) member() Node {
	node := p.primary()
	if node != nil {
		for p.accept(".") != nil {
			id := NewIdent(stringOf(p.expect("ident").Val), nil, false)
			node = NewMember(node, id)
		}
		p.skipSpaces()
		if p.accept("=") != nil {
			val := p.list()
			switch n := node.(type) {
			case *MemberNode:
				n.Val = val
			case *IdentNode:
				n.Val = val
			case *BinOpNode:
				n.Val = val
			}
			if e, ok := val.(*ExpressionNode); ok && e.IsEmpty() {
				p.assignAtblock(e)
			}
		}
	}
	return node
}

func (p *Parser) object() Node {
	obj := NewObject()
	var comma bool
	p.expect("{")
	p.skipWhitespace()
	for p.accept("}") == nil {
		if p.accept("comment") != nil || p.accept("newline") != nil {
			continue
		}
		if !comma {
			p.accept(",")
		}
		id := p.accept("ident")
		if id == nil {
			id = p.accept("string")
		}
		if id == nil {
			p.error("expected \"ident\" or \"string\", got {peek}")
		}
		hash := hashKey(id.Val.Hash())
		p.skipSpacesAndComments()
		p.expect(":")
		val := p.expression()
		obj.SetValue(hash, val)
		obj.SetKey(hash, id.Val)
		comma = p.accept(",") != nil
		p.skipWhitespace()
	}
	return obj
}

func (p *Parser) primary() Node {
	p.skipSpaces()

	if p.accept("(") != nil {
		p.parens++
		expr := p.expression()
		paren := p.expect(")")
		p.parens--
		if p.accept("%") != nil {
			expr.Push(NewIdent("%", nil, false))
		}
		tok := p.peek()
		if paren.Space == "" && tok.Type == "ident" && cssUnits[stringOf(tok.Val)] {
			expr.Push(NewIdent(stringOf(tok.Val), nil, false))
			p.next()
		}
		return expr
	}

	tok := p.peek()
	switch tok.Type {
	case "null", "unit", "color", "string", "literal", "boolean", "comment":
		return p.next().Val
	case "{":
		if !p.cond {
			return p.object()
		}
	case "atblock":
		return p.atblock(false)
	case "atrule":
		id := NewIdent(p.next().StrVal, nil, false)
		id.Property = true
		return id
	case "ident":
		return p.ident()
	case "function":
		if tok.Anonymous {
			return p.functionDefinition()
		}
		return p.functionCall()
	}
	return nil
}

// List parses a comma separated list; used by utils.parseString.
func (p *Parser) List() Node { return p.list() }
