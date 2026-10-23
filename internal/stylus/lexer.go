/*!
 * Stylus - Lexer
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go. The original matched each token with a
 * JavaScript regular expression; several of them use lookahead, which Go's
 * regexp package does not have, so the port scans the input by hand and keeps
 * the original's ordering, backtracking and skip arithmetic.
 */

package stylus

import (
	"strings"
)

// Operator aliases.
var lexerAlias = map[string]string{
	"and":    "&&",
	"or":     "||",
	"is":     "==",
	"isnt":   "!=",
	"is not": "!=",
	":=":     "?=",
}

// Token is one lexed token.
type Token struct {
	Type      string
	Val       Node
	StrVal    string // for tokens whose value is a plain string
	HasStrVal bool
	Lineno    int
	Column    int
	Space     string
	Anonymous bool
}

func (t *Token) String() string {
	if t.HasStrVal {
		return t.StrVal
	}
	if t.Val != nil {
		return t.Val.String()
	}
	return t.Type
}

func newToken(typ string) *Token { return &Token{Type: typ} }

func newTokenVal(typ string, val Node) *Token { return &Token{Type: typ, Val: val} }

func newTokenStr(typ, val string) *Token {
	return &Token{Type: typ, StrVal: val, HasStrVal: true}
}

// Lexer turns Stylus source into tokens.
type Lexer struct {
	Str         string
	stash       []*Token
	indentStack []int
	indentRe    int // 0 unspecified, 1 tabs, 2 spaces
	Lineno      int
	Column      int
	prev        *Token
	isURL       bool
}

// NewLexer prepares `str` the way the original's constructor did.
func NewLexer(str string) *Lexer {
	l := &Lexer{Lineno: 1, Column: 1}
	if strings.HasPrefix(str, "\ufeff") {
		str = str[len("\ufeff"):]
	}
	str = replaceTrailingSpace(str)
	str = normaliseNewlines(str)
	str = replaceLineContinuations(str)
	str = joinAfterSeparators(str)
	str = joinBeforeSeparators(str)
	l.Str = str
	return l
}

// jsIsSpace is JavaScript's \s character class.
func jsIsSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// replaceTrailingSpace is str.replace(/\s+$/, '\n').
func replaceTrailingSpace(s string) string {
	r := []rune(s)
	i := len(r)
	for i > 0 && jsIsSpace(r[i-1]) {
		i--
	}
	if i == len(r) {
		return s
	}
	return string(r[:i]) + "\n"
}

// normaliseNewlines is str.replace(/\r\n?/g, '\n').
func normaliseNewlines(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			b.WriteByte('\n')
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// replaceLineContinuations is str.replace(/\\ *\n/g, '\r').
func replaceLineContinuations(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			j := i + 1
			for j < len(s) && s[j] == ' ' {
				j++
			}
			if j < len(s) && s[j] == '\n' {
				b.WriteByte('\r')
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// commentReplace is the `comment` helper of the original's constructor: it
// decides whether a match sits inside a comment and, if not, what replaces it.
func commentReplace(match, val string, offset int, s string) string {
	inComment := lastIndexFrom(s, "/*", offset) > lastIndexFrom(s, "*/", offset)
	commentIdx := lastIndexFrom(s, "//", offset)
	i := lastIndexFrom(s, "\n", offset)
	double, single := 0, 0

	if commentIdx != -1 && commentIdx > i {
		for i != offset {
			if i >= 0 && i < len(s) {
				if s[i] == '\'' {
					if single != 0 {
						single--
					} else {
						single++
					}
				}
				if s[i] == '"' {
					if double != 0 {
						double--
					} else {
						double++
					}
				}
				if s[i] == '/' && i+1 < len(s) && s[i+1] == '/' {
					inComment = single == 0 && double == 0
					break
				}
			}
			i++
			if i > len(s) {
				break
			}
		}
	}

	if inComment {
		return match
	}
	if val == "," && onlyCommasTabsNewlines(match) {
		return strings.Replace(match, "\n", "\r", 1)
	}
	return val + "\r"
}

// lastIndexFrom is String#lastIndexOf(needle, from).
func lastIndexFrom(s, needle string, from int) int {
	if from < 0 {
		from = 0
	}
	end := from + len(needle)
	if end > len(s) {
		end = len(s)
	}
	return strings.LastIndex(s[:end], needle)
}

// onlyCommasTabsNewlines is /^[,\t\n]+$/.
func onlyCommasTabsNewlines(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ',', '\t', '\n':
		default:
			return false
		}
	}
	return true
}

// joinAfterSeparators is the replace of
// /([,(:](?!\/\/[^ ])) *(?:\/\/[^\n]*|\/\*.*?\*\/)?\n\s*/g with `comment`.
func joinAfterSeparators(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ',' || c == '(' || c == ':' {
			if !negLookaheadSlashSlash(s, i+1) {
				if end, ok := matchJoinAfter(s, i); ok {
					match := s[i:end]
					b.WriteString(commentReplace(match, string(c), i, s))
					i = end
					continue
				}
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// negLookaheadSlashSlash reports whether `//` followed by a non-space sits at i,
// which is what (?!\/\/[^ ]) rejects.
func negLookaheadSlashSlash(s string, i int) bool {
	return i+2 < len(s) && s[i] == '/' && s[i+1] == '/' && s[i+2] != ' '
}

// matchJoinAfter matches ` *(?:\/\/[^\n]*|\/\*.*?\*\/)?\n\s*` after s[start].
func matchJoinAfter(s string, start int) (int, bool) {
	j := start + 1
	for j < len(s) && s[j] == ' ' {
		j++
	}
	// The group is ordered and optional: try `//`, then `/*...*/`, then nothing.
	var tries []int
	if j+1 < len(s) && s[j] == '/' && s[j+1] == '/' {
		k := j + 2
		for k < len(s) && s[k] != '\n' {
			k++
		}
		tries = append(tries, k)
	}
	if j+1 < len(s) && s[j] == '/' && s[j+1] == '*' {
		for k := j + 2; k+1 < len(s); k++ {
			if s[k] == '\n' {
				break
			}
			if s[k] == '*' && s[k+1] == '/' {
				tries = append(tries, k+2)
				break
			}
		}
	}
	tries = append(tries, j)
	for _, k := range tries {
		if k < len(s) && s[k] == '\n' {
			k++
			for k < len(s) {
				r, size := decodeRune(s[k:])
				if !jsIsSpace(r) {
					break
				}
				k += size
			}
			return k, true
		}
	}
	return 0, false
}

// joinBeforeSeparators is the replace of /\s*\n[ \t]*([,)])/g with `comment`.
func joinBeforeSeparators(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if end, val, ok := matchJoinBefore(s, i); ok {
			b.WriteString(commentReplace(s[i:end], val, i, s))
			i = end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// matchJoinBefore looks for \s*\n[ \t]*([,)]) with its match starting at `at`.
func matchJoinBefore(s string, at int) (int, string, bool) {
	k := at
	for k < len(s) {
		r, size := decodeRune(s[k:])
		if !jsIsSpace(r) {
			break
		}
		k += size
	}
	for j := k; j >= at; j-- {
		if j < len(s) && s[j] == '\n' {
			m := j + 1
			for m < len(s) && (s[m] == ' ' || s[m] == '\t') {
				m++
			}
			if m < len(s) && (s[m] == ',' || s[m] == ')') {
				return m + 1, string(s[m]), true
			}
		}
	}
	return 0, "", false
}

func decodeRune(s string) (rune, int) {
	for _, r := range s {
		size := 1
		for size < len(s) && s[size]&0xc0 == 0x80 {
			size++
		}
		return r, size
	}
	return 0, 0
}

/*
 * Token scanning
 */

func (l *Lexer) Lookahead(n int) *Token {
	fetch := n - len(l.stash)
	for fetch > 0 {
		l.stash = append(l.stash, l.advance())
		fetch--
	}
	return l.stash[n-1]
}

func (l *Lexer) Peek() *Token { return l.Lookahead(1) }

func (l *Lexer) Next() *Token {
	var tok *Token
	if len(l.stash) > 0 {
		tok = l.stash[0]
		l.stash = l.stash[1:]
	} else {
		tok = l.advance()
	}
	l.prev = tok
	return tok
}

// skipStr consumes `chunk` and tracks the line and column as the original did.
func (l *Lexer) skipStr(chunk string) {
	l.Str = l.Str[len(chunk):]
	if chunk != "" {
		l.move(chunk)
	}
}

func (l *Lexer) skipN(n int) {
	l.Str = l.Str[n:]
	l.Column += n
}

func (l *Lexer) move(str string) {
	lines := strings.Count(str, "\n")
	idx := strings.LastIndex(str, "\n")
	if lines > 0 {
		l.Lineno += lines
	}
	if idx >= 0 {
		l.Column = len(str) - idx
	} else {
		l.Column += len(str)
	}
}

func (l *Lexer) isPartOfSelector() bool {
	var tok *Token
	if len(l.stash) > 0 {
		tok = l.stash[len(l.stash)-1]
	} else {
		tok = l.prev
	}
	if tok == nil {
		return false
	}
	switch tok.Type {
	case "color":
		if c, ok := tok.Val.(*RGBANode); ok {
			return len(c.Raw) == 2
		}
		return false
	case ".", "[":
		return true
	}
	return false
}

func (l *Lexer) advance() *Token {
	column := l.Column
	line := l.Lineno

	steps := []func() *Token{
		l.eos, l.lexNull, l.sep, l.keyword, l.urlchars, l.comment, l.newline,
		l.escaped, l.important, l.literal, l.anonFunc, l.atrule, l.function,
		l.brace, l.paren, l.color, l.lexString, l.unit, l.namedop, l.boolean,
		l.unicode, l.ident, l.op,
	}
	var tok *Token
	for _, step := range steps {
		if tok = step(); tok != nil {
			break
		}
	}
	if tok == nil {
		if t := l.eol(); t != nil {
			column = t.Column
			line = t.Lineno
			tok = t
		}
	}
	if tok == nil {
		tok = l.space()
	}
	if tok == nil {
		tok = l.selector()
	}

	tok.Lineno = line
	tok.Column = column
	return tok
}

func (l *Lexer) eos() *Token {
	if len(l.Str) > 0 {
		return nil
	}
	if len(l.indentStack) > 0 {
		l.indentStack = l.indentStack[1:]
		return newToken("outdent")
	}
	return newToken("eos")
}

func (l *Lexer) urlchars() *Token {
	if !l.isURL {
		return nil
	}
	i := 0
	for i < len(l.Str) && strings.IndexByte("/:@.;?&=*!,<>#%0123456789", l.Str[i]) >= 0 {
		i++
	}
	if i == 0 {
		return nil
	}
	chunk := l.Str[:i]
	l.skipStr(chunk)
	return newTokenVal("literal", NewLiteral(chunk))
}

func (l *Lexer) sep() *Token {
	if len(l.Str) == 0 || l.Str[0] != ';' {
		return nil
	}
	i := 1
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	l.skipStr(l.Str[:i])
	return newToken(";")
}

func (l *Lexer) eol() *Token {
	if len(l.Str) == 0 || l.Str[0] != '\r' {
		return nil
	}
	l.Lineno++
	l.skipN(1)
	l.Column = 1
	for l.space() != nil {
	}
	return l.advance()
}

func (l *Lexer) space() *Token {
	i := 0
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	if i == 0 {
		return nil
	}
	l.skipStr(l.Str[:i])
	return newToken("space")
}

func (l *Lexer) escaped() *Token {
	if len(l.Str) < 2 || l.Str[0] != '\\' {
		return nil
	}
	r, size := decodeRune(l.Str[1:])
	if r == '\n' {
		return nil
	}
	c := l.Str[1 : 1+size]
	i := 1 + size
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	l.skipStr(l.Str[:i])
	return newTokenVal("ident", NewLiteral(c))
}

func (l *Lexer) literal() *Token {
	if !strings.HasPrefix(l.Str, "@css") {
		return nil
	}
	i := 4
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	if i >= len(l.Str) || l.Str[i] != '{' {
		return nil
	}
	l.skipStr(l.Str[:i+1])
	braces := 1
	var css strings.Builder
	for len(l.Str) > 0 {
		c := l.Str[0]
		l.Str = l.Str[1:]
		switch c {
		case '{':
			braces++
		case '}':
			braces--
		case '\n', '\r':
			l.Lineno++
		}
		css.WriteByte(c)
		if braces == 0 {
			break
		}
	}
	node := NewLiteral(trimTrailingSpaceBrace(css.String()))
	node.CSS = true
	return newTokenVal("literal", node)
}

// trimTrailingSpaceBrace is replace(/\s*}$/, ”).
func trimTrailingSpaceBrace(s string) string {
	if !strings.HasSuffix(s, "}") {
		return s
	}
	r := []rune(s[:len(s)-1])
	i := len(r)
	for i > 0 && jsIsSpace(r[i-1]) {
		i--
	}
	return string(r[:i])
}

func (l *Lexer) important() *Token {
	if !strings.HasPrefix(l.Str, "!important") {
		return nil
	}
	i := len("!important")
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	l.skipStr(l.Str[:i])
	return newTokenVal("ident", NewLiteral("!important"))
}

func (l *Lexer) brace() *Token {
	if len(l.Str) == 0 || (l.Str[0] != '{' && l.Str[0] != '}') {
		return nil
	}
	brace := string(l.Str[0])
	l.skipN(1)
	return newTokenStr(brace, brace)
}

func (l *Lexer) paren() *Token {
	if len(l.Str) == 0 || (l.Str[0] != '(' && l.Str[0] != ')') {
		return nil
	}
	paren := string(l.Str[0])
	i := 1
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	space := l.Str[1:i]
	l.skipStr(l.Str[:i])
	if paren == ")" {
		l.isURL = false
	}
	tok := newTokenStr(paren, paren)
	tok.Space = space
	return tok
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// wordBoundaryAfter is \b at index i, given a word character before it.
func (l *Lexer) wordBoundaryAfter(i int) bool {
	return i >= len(l.Str) || !isWordByte(l.Str[i])
}

func (l *Lexer) lexNull() *Token {
	if !strings.HasPrefix(l.Str, "null") || !l.wordBoundaryAfter(4) {
		return nil
	}
	i := 4
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	matched := l.Str[:i]
	l.skipStr(matched)
	if l.isPartOfSelector() {
		return newTokenVal("ident", NewIdent(matched, nil, false))
	}
	return newTokenVal("null", NullNode_)
}

var lexerKeywords = []string{"return", "if", "else", "unless", "for", "in"}

func (l *Lexer) keyword() *Token {
	for _, kw := range lexerKeywords {
		if !strings.HasPrefix(l.Str, kw) {
			continue
		}
		n := len(kw)
		if !l.wordBoundaryAfter(n) {
			continue
		}
		if n < len(l.Str) && l.Str[n] == '-' {
			continue
		}
		i := n
		for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
			i++
		}
		matched := l.Str[:i]
		l.skipStr(matched)
		if l.isPartOfSelector() {
			return newTokenVal("ident", NewIdent(matched, nil, false))
		}
		return newTokenStr(kw, kw)
	}
	return nil
}

var lexerNamedOps = []string{"not", "and", "or", "is a", "is defined", "isnt", "is not", "is"}

func (l *Lexer) namedop() *Token {
	for _, name := range lexerNamedOps {
		if !strings.HasPrefix(l.Str, name) {
			continue
		}
		n := len(name)
		if n < len(l.Str) && l.Str[n] == '-' {
			continue
		}
		if !l.wordBoundaryAfter(n) {
			continue
		}
		i := n
		for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
			i++
		}
		space := l.Str[n:i]
		matched := l.Str[:i]
		l.skipStr(matched)
		if l.isPartOfSelector() {
			tok := newTokenVal("ident", NewIdent(matched, nil, false))
			tok.Space = space
			return tok
		}
		op := name
		if a, ok := lexerAlias[op]; ok {
			op = a
		}
		tok := newTokenStr(op, op)
		tok.Space = space
		return tok
	}
	return nil
}

func (l *Lexer) op() *Token {
	s := l.Str
	op := ""
	switch {
	case strings.HasPrefix(s, "..."):
		op = "..."
	case strings.HasPrefix(s, ".."):
		op = ".."
	case strings.HasPrefix(s, "."):
		op = "."
	case strings.HasPrefix(s, "&&"):
		op = "&&"
	case strings.HasPrefix(s, "||"):
		op = "||"
	case len(s) >= 2 && strings.IndexByte("!<>=?:", s[0]) >= 0 && s[1] == '=':
		op = s[:2]
	case strings.HasPrefix(s, "**"):
		op = "**"
	case len(s) >= 1 && strings.IndexByte("-+*/%", s[0]) >= 0:
		if len(s) >= 2 && s[1] == '=' {
			op = s[:2]
		} else {
			op = s[:1]
		}
	case len(s) >= 1 && strings.IndexByte(",=?:!~<>&[]", s[0]) >= 0:
		op = s[:1]
	default:
		return nil
	}
	i := len(op)
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	space := s[len(op):i]
	l.skipStr(s[:i])
	if a, ok := lexerAlias[op]; ok {
		op = a
	}
	tok := newTokenStr(op, op)
	tok.Space = space
	l.isURL = false
	return tok
}

func (l *Lexer) anonFunc() *Token {
	if len(l.Str) >= 2 && l.Str[0] == '@' && l.Str[1] == '(' {
		l.skipN(2)
		tok := newTokenVal("function", NewIdent("anonymous", nil, false))
		tok.Anonymous = true
		return tok
	}
	return nil
}

func (l *Lexer) atrule() *Token {
	if len(l.Str) == 0 || l.Str[0] != '@' {
		return nil
	}
	if strings.HasPrefix(l.Str[1:], "apply") {
		return nil
	}
	i := 1
	vendor := ""
	if i < len(l.Str) && l.Str[i] == '-' {
		j := i + 1
		for j < len(l.Str) && isWordByte(l.Str[j]) {
			j++
		}
		if j > i+1 && j < len(l.Str) && l.Str[j] == '-' {
			vendor = l.Str[i+1 : j]
			i = j + 1
		}
	}
	start := i
	for i < len(l.Str) && isAtruleByte(l.Str[i]) {
		i++
	}
	if i == start {
		return nil
	}
	typ := l.Str[start:i]
	for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
		i++
	}
	l.skipStr(l.Str[:i])
	switch typ {
	case "require", "import", "charset", "namespace", "media", "scope", "supports":
		return newToken(typ)
	case "document":
		return newToken("-moz-document")
	case "block":
		return newToken("atblock")
	case "extend", "extends":
		return newToken("extend")
	case "keyframes":
		return newTokenStr(typ, vendor)
	default:
		if vendor != "" {
			return newTokenStr("atrule", "-"+vendor+"-"+typ)
		}
		return newTokenStr("atrule", typ)
	}
}

func isAtruleByte(c byte) bool {
	return c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (l *Lexer) comment() *Token {
	if len(l.Str) >= 2 && l.Str[0] == '/' && l.Str[1] == '/' {
		end := strings.IndexByte(l.Str, '\n')
		if end == -1 {
			end = len(l.Str)
		}
		l.skipStr(l.Str[:end])
		return l.advance()
	}
	if len(l.Str) >= 2 && l.Str[0] == '/' && l.Str[1] == '*' {
		end := strings.Index(l.Str, "*/")
		if end == -1 {
			end = len(l.Str)
		}
		stop := end + 2
		if stop > len(l.Str) {
			stop = len(l.Str)
		}
		str := l.Str[:stop]
		lines := strings.Count(str, "\n") + strings.Count(str, "\r")
		suppress := true
		inline := false
		l.Lineno += lines
		l.skipStr(str)
		if len(str) > 2 && str[2] == '!' {
			str = strings.Replace(str, "*!", "*", 1)
			suppress = false
		}
		if l.prev != nil && l.prev.Type == ";" {
			inline = true
		}
		return newTokenVal("comment", NewComment(str, suppress, inline))
	}
	return nil
}

func (l *Lexer) boolean() *Token {
	for _, word := range []string{"true", "false"} {
		if !strings.HasPrefix(l.Str, word) || !l.wordBoundaryAfter(len(word)) {
			continue
		}
		i := len(word)
		for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
			i++
		}
		space := l.Str[len(word):i]
		val := NewBoolean(word == "true")
		l.skipStr(l.Str[:i])
		tok := newTokenVal("boolean", val)
		tok.Space = space
		return tok
	}
	return nil
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (l *Lexer) unicode() *Token {
	s := l.Str
	if len(s) < 3 || (s[0] != 'u' && s[0] != 'U') || s[1] != '+' {
		return nil
	}
	i := 2
	n := 0
	for i < len(s) && n < 6 && (isHexByte(s[i]) || s[i] == '?') {
		i++
		n++
	}
	if n == 0 {
		return nil
	}
	if i < len(s) && s[i] == '-' {
		j := i + 1
		m := 0
		for j < len(s) && m < 6 && isHexByte(s[j]) {
			j++
			m++
		}
		if m > 0 {
			i = j
		}
	}
	chunk := s[:i]
	l.skipStr(chunk)
	return newTokenVal("literal", NewLiteral(chunk))
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentByte(c byte) bool {
	return c == '-' || c == '$' || isWordByte(c)
}

func (l *Lexer) function() *Token {
	s := l.Str
	i := 0
	for i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) || !isIdentStart(s[i]) {
		return nil
	}
	i++
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '(' {
		return nil
	}
	name := s[:i]
	i++
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	space := s[len(name)+1 : i]
	l.skipStr(s[:i])
	l.isURL = name == "url"
	tok := newTokenVal("function", NewIdent(name, nil, false))
	tok.Space = space
	return tok
}

func (l *Lexer) ident() *Token {
	s := l.Str
	i := 0
	for i < len(s) && s[i] == '-' {
		i++
	}
	if i < len(s) && isIdentStart(s[i]) {
		i++
	} else if strings.HasPrefix(s[i:], "@apply") {
		i += len("@apply")
	} else {
		return nil
	}
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	chunk := s[:i]
	l.skipStr(chunk)
	return newTokenVal("ident", NewIdent(chunk, nil, false))
}

func (l *Lexer) newline() *Token {
	if len(l.Str) == 0 || l.Str[0] != '\n' {
		return nil
	}
	var indents, matchLen int
	switch l.indentRe {
	case 1: // /^\n([\t]*)[ \t]*/
		i := 1
		for i < len(l.Str) && l.Str[i] == '\t' {
			i++
		}
		indents = i - 1
		for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
			i++
		}
		matchLen = i
	case 2: // /^\n([ \t]*)/
		i := 1
		for i < len(l.Str) && (l.Str[i] == ' ' || l.Str[i] == '\t') {
			i++
		}
		indents = i - 1
		matchLen = i
	default:
		i := 1
		for i < len(l.Str) && l.Str[i] == '\t' {
			i++
		}
		tabs := i - 1
		if tabs > 0 {
			j := i
			for j < len(l.Str) && (l.Str[j] == ' ' || l.Str[j] == '\t') {
				j++
			}
			indents, matchLen, l.indentRe = tabs, j, 1
		} else {
			j := 1
			for j < len(l.Str) && (l.Str[j] == ' ' || l.Str[j] == '\t') {
				j++
			}
			indents, matchLen = j-1, j
			if indents > 0 {
				l.indentRe = 2
			}
		}
	}

	l.skipStr(l.Str[:matchLen])
	if len(l.Str) > 0 && (l.Str[0] == ' ' || l.Str[0] == '\t') {
		throwNamed("SyntaxError", "Invalid indentation. You can use tabs or spaces to indent, but not both.")
	}

	// Blank line
	if len(l.Str) > 0 && l.Str[0] == '\n' {
		return l.advance()
	}

	var tok *Token
	switch {
	case len(l.indentStack) > 0 && indents < l.indentStack[0]:
		for len(l.indentStack) > 0 && l.indentStack[0] > indents {
			l.stash = append(l.stash, newToken("outdent"))
			l.indentStack = l.indentStack[1:]
		}
		tok = l.stash[len(l.stash)-1]
		l.stash = l.stash[:len(l.stash)-1]
	case indents > 0 && (len(l.indentStack) == 0 || indents != l.indentStack[0]):
		l.indentStack = append([]int{indents}, l.indentStack...)
		tok = newToken("indent")
	default:
		tok = newToken("newline")
	}
	return tok
}

func (l *Lexer) unit() *Token {
	s := l.Str
	i := 0
	neg := false
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	start := i
	intDigits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		intDigits++
	}
	numEnd := i
	if i < len(s) && s[i] == '.' {
		j := i + 1
		frac := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			frac++
		}
		if frac > 0 && (intDigits > 0 || frac > 0) {
			if intDigits > 0 {
				numEnd = j
			} else {
				numEnd = j
			}
		}
	}
	if numEnd == start {
		return nil
	}
	numStr := s[start:numEnd]
	i = numEnd
	typ := ""
	if i < len(s) && s[i] == '%' {
		typ = "%"
		i++
	} else {
		j := i
		for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
			j++
		}
		if j > i {
			typ = s[i:j]
			i = j
		}
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	raw := s[:i]
	l.skipStr(raw)
	n := parseFloatJS(numStr)
	if neg {
		n = -n
	}
	node := NewUnit(n, typ)
	node.Raw = raw
	return newTokenVal("unit", node)
}

func (l *Lexer) lexString() *Token {
	s := l.Str
	if len(s) == 0 || (s[0] != '"' && s[0] != '\'') {
		return nil
	}
	quote := s[0]
	end := strings.IndexByte(s[1:], quote)
	if end == -1 {
		return nil
	}
	end++
	str := s[1:end]
	i := end + 1
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	l.skipStr(s[:i])
	str = strings.ReplaceAll(str, "\\n", "\n")
	return newTokenVal("string", NewString(str, string(quote)))
}

func (l *Lexer) color() *Token {
	for _, n := range []int{8, 6, 4, 3, 2, 1} {
		if tok := l.hexColor(n); tok != nil {
			return tok
		}
	}
	return nil
}

func (l *Lexer) hexColor(n int) *Token {
	s := l.Str
	if len(s) < n+1 || s[0] != '#' {
		return nil
	}
	for i := 1; i <= n; i++ {
		if !isHexByte(s[i]) {
			return nil
		}
	}
	hex := s[1 : n+1]
	i := n + 1
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	raw := s[:i]
	l.skipStr(raw)
	hx := func(x string) float64 { return parseIntJS(x, 16) }
	var color *RGBANode
	switch n {
	case 1:
		v := hx(hex + hex)
		color = NewRGBA(v, v, v, 1)
	case 2:
		v := hx(hex)
		color = NewRGBA(v, v, v, 1)
	case 3:
		color = NewRGBA(hx(hex[0:1]+hex[0:1]), hx(hex[1:2]+hex[1:2]), hx(hex[2:3]+hex[2:3]), 1)
	case 4:
		color = NewRGBA(hx(hex[0:1]+hex[0:1]), hx(hex[1:2]+hex[1:2]), hx(hex[2:3]+hex[2:3]), hx(hex[3:4]+hex[3:4])/255)
	case 6:
		color = NewRGBA(hx(hex[0:2]), hx(hex[2:4]), hx(hex[4:6]), 1)
	case 8:
		color = NewRGBA(hx(hex[0:2]), hx(hex[2:4]), hx(hex[4:6]), hx(hex[6:8])/255)
	}
	color.Raw = raw
	return newTokenVal("color", color)
}

// selector is /^\^|.*?(?=\/\/(?![^\[]*\])|[,\n{])/.
func (l *Lexer) selector() *Token {
	s := l.Str
	if strings.HasPrefix(s, "^") {
		l.skipStr("^")
		return newTokenStr("selector", "^")
	}
	for start := 0; start <= len(s); start++ {
		for k := start; k <= len(s); k++ {
			if k > start && s[k-1] == '\n' {
				break
			}
			if selectorLookahead(s, k) {
				matched := s[start:k]
				l.skipStr(matched)
				return newTokenStr("selector", matched)
			}
		}
	}
	l.skipStr(s)
	return newTokenStr("selector", s)
}

// selectorLookahead is (?=\/\/(?![^\[]*\])|[,\n{]) at index i.
func selectorLookahead(s string, i int) bool {
	if i < len(s) {
		switch s[i] {
		case ',', '\n', '{':
			return true
		}
	}
	if i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
		rest := s[i+2:]
		for j := 0; j < len(rest); j++ {
			if rest[j] == '[' {
				break
			}
			if rest[j] == ']' {
				return false
			}
		}
		return true
	}
	return false
}
