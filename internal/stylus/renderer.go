/*!
 * Stylus - Renderer
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import (
	_ "embed"
	"io"
	"os"
)

// coreLibrary is lib/functions/index.styl, the part of the built-in library
// written in Stylus itself; the original imported it from disk.
//
//go:embed index.styl
var coreLibrary string

// stdout and stderr are the streams the built-ins write to; tests replace them.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// Options are the renderer options the CLI sets.
type Options struct {
	Filename          string
	Paths             []string
	Imports           []string
	Globals           map[string]Node
	Prefix            string
	Compress          bool
	Firebug           bool
	Linenos           bool
	IncludeCSS        bool
	ResolveURL        bool
	ResolveURLNocheck bool
	HoistAtrules      bool
	IndentSpaces      int
	Warn              bool
	Dest              string
	URLLimit          int
	URLPaths          []string
	EnableURL         bool
}

// Renderer compiles one stylesheet.
type Renderer struct {
	Str     string
	Options *Options
}

// NewRenderer prepares a render of `str`.
func NewRenderer(str string, options *Options) *Renderer {
	if options == nil {
		options = &Options{}
	}
	if options.Globals == nil {
		options.Globals = map[string]Node{}
	}
	if options.Filename == "" {
		options.Filename = "stylus"
	}
	options.Imports = append([]string{coreLibraryPath}, options.Imports...)
	return &Renderer{Str: str, Options: options}
}

// coreLibraryPath is the pseudo-path of the embedded core library.
const coreLibraryPath = "/opt/stylus/lib/functions/index.styl"

// Render compiles the stylesheet, returning the CSS or a formatted error.
func (r *Renderer) Render() (css string, rerr *JSError) {
	parserCache = nil
	resetGlobals()
	opts := r.Options
	sprintfOptions = opts

	var parser *Parser
	err := catchAny(func() {
		curFilename = opts.Filename
		parser = NewParser(r.Str, ParserOptions{Prefix: opts.Prefix, Root: NewRoot()})
		ast := parser.Parse()

		ev := NewEvaluator(ast, opts)
		if opts.EnableURL {
			ev.Functions["url"] = NewBuiltin("url", func(e *Evaluator, args []Node) Node {
				return urlFn(e, args, opts.URLPaths, opts.URLLimit)
			}, []string{"url", "enc"}, true)
		}
		if opts.ResolveURL {
			ev.Functions["url"] = NewBuiltin("url", func(e *Evaluator, args []Node) Node {
				return resolverFn(e, args, opts)
			}, []string{"url"}, true)
		}
		ev.Warn = func(msg string) { os.Stderr.WriteString(msg + "\n") }
		evaluated := ev.Evaluate()

		normalizer := NewNormalizer(evaluated, opts)
		normalized := normalizer.Normalize()

		compiler := NewCompiler(normalized, opts)
		css = compiler.Compile()
	})
	if err != nil {
		input := err.Input
		if input == "" {
			input = r.Str
		}
		filename := err.Filename
		if filename == "" {
			filename = opts.Filename
		}
		lineno := err.Lineno
		column := err.Column
		if lineno == 0 && parser != nil && parser.lexer != nil {
			lineno = parser.lexer.Lineno
		}
		if column == 0 && parser != nil && parser.lexer != nil {
			column = parser.lexer.Column
		}
		return "", formatException(err, lineno, column, filename, input, 8)
	}
	return css, nil
}

func resetGlobals() {
	curLineno = 0
	curColumn = 0
	curFilename = ""
}

// resolverFn is lib/functions/resolver.js, the url() used by --resolve-url.
func resolverFn(ev *Evaluator, args []Node, opts *Options) Node {
	urlNode := arg(args, 0)
	filename := ""
	if urlNode != nil {
		filename = urlNode.base().Filename
	}
	compiler := NewCompiler(nil, nil)
	compiler.isURL = true
	var b strings_Builder
	for _, node := range nodesOf(urlNode) {
		b.WriteString(compiler.visit(node))
	}
	raw := b.String()
	pathname, search, hash := splitURL(raw)
	literal := NewLiteral("url(\"" + raw + "\")")
	if pathname == "" || pathname[0] == '/' || hasProtocol(raw) {
		return literal
	}
	p := pathname
	if !opts.ResolveURLNocheck {
		found, ok := lookup(p, append(append([]string{}, opts.URLPaths...), ev.paths...), "")
		if !ok {
			return literal
		}
		p = found
	}
	if ev.includeCSS && pathExt(p) == ".css" {
		return NewLiteral(raw)
	}
	tail := search + hash
	dest := opts.Dest
	if dest != "" && pathExt(dest) == ".css" {
		dest = pathDir(dest)
	}
	base := dest
	if base == "" {
		base = pathDir(ev.filename)
	}
	target := p
	if opts.ResolveURLNocheck {
		target = pathJoin(pathDir(filename), p)
	}
	res := pathRelative(base, target) + tail
	return NewLiteral("url(\"" + res + "\")")
}

func hasProtocol(raw string) bool {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == ':' {
			return i > 0
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return false
}
