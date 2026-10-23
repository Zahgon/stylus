/*!
 * Stylus - command line interface
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript (bin/stylus) to Go.
 */

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stylus/stylus/internal/stylus"
)

const version = "0.64.0"

const usage = `
  Usage: stylus [options] [command] [< in [> out]]
                [file|dir ...]

  Commands:

    help [<type>:]<prop> Opens help info at MDN for <prop> in
                         your default browser. Optionally
                         searches other resources of <type>:
                         safari opera w3c ms caniuse quirksmode

  Options:

    -i, --interactive       Start interactive REPL
    -u, --use <path>        Utilize the Stylus plugin at <path>
    -U, --inline            Utilize image inlining via data URI support
    -w, --watch             Watch file(s) for changes and re-compile
    -o, --out <dir>         Output to <dir> when passing files
    -C, --css <src> [dest]  Convert CSS input to Stylus
    -I, --include <path>    Add <path> to lookup paths
    -c, --compress          Compress CSS output
    -d, --compare           Display input along with output
    -f, --firebug           Emits debug infos in the generated CSS that
                            can be used by the FireStylus Firebug plugin
    -l, --line-numbers      Emits comments in the generated CSS
                            indicating the corresponding Stylus line
    -m, --sourcemap         Generates a sourcemap in sourcemaps v3 format
    -q, --quiet     				 Less noisy output
    --sourcemap-inline      Inlines sourcemap with full source text in base64 format
    --sourcemap-root <url>  "sourceRoot" property of the generated sourcemap
    --sourcemap-base <path> Base <path> from which sourcemap and all sources are relative
    -P, --prefix [prefix]   prefix all css classes
    -p, --print             Print out the compiled CSS
    --import <file>         Import stylus <file>
    --include-css           Include regular CSS on @import
	 --ext			 Specify custom file extension for compiled file, default .css	
    -D, --deps              Display dependencies of the compiled file
    --disable-cache         Disable caching
    --hoist-atrules         Move @import and @charset to the top
    -r, --resolve-url       Resolve relative urls inside imports
    --resolve-url-nocheck   Like --resolve-url but without file existence check
    -V, --version           Display the version of Stylus
    -h, --help              Display help information
`

type cli struct {
	compare           bool
	compress          bool
	linenos           bool
	firebug           bool
	print             bool
	prefix            string
	files             []string
	paths             []string
	dest              string
	includeCSS        bool
	imports           []string
	resolveURL        bool
	resolveURLNocheck bool
	hoist             bool
	ext               string
	urlFunction       bool
	urlLimit          int
}

func main() {
	os.Exit(run())
}

// throwCLI reproduces an uncaught Error thrown by the original's argument
// handling: the message on standard error and exit status 1.
func throwCLI(msg string) int {
	fmt.Fprintln(os.Stderr, "Error: "+msg)
	return 1
}

func run() int {
	c := &cli{ext: ".css", urlLimit: 30000}
	args := os.Args[1:]
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		shift := func() string {
			if len(args) == 0 {
				return ""
			}
			v := args[0]
			args = args[1:]
			return v
		}
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, strings.TrimSuffix(usage, "\n"))
			return 0
		case "-d", "--compare":
			c.compare = true
		case "-c", "--compress":
			c.compress = true
		case "-C", "--css":
			fmt.Fprintln(os.Stderr, "Error: --css is not supported")
			return 1
		case "-f", "--firebug":
			c.firebug = true
		case "-l", "--line-numbers":
			c.linenos = true
		case "-m", "--sourcemap", "--sourcemap-inline":
			// Source maps are out of scope.
		case "-q", "--quiet":
		case "--sourcemap-root":
			if shift() == "" {
				return throwCLI("--sourcemap-root <url> required")
			}
		case "--sourcemap-base":
			if shift() == "" {
				return throwCLI("--sourcemap-base <path> required")
			}
		case "-P", "--prefix":
			c.prefix = shift()
			if c.prefix == "" {
				return throwCLI("--prefix <prefix> required")
			}
		case "-p", "--print":
			c.print = true
		case "-V", "--version":
			fmt.Fprintln(os.Stdout, version)
			return 0
		case "-o", "--out":
			c.dest = shift()
			if c.dest == "" {
				return throwCLI("--out <dir> required")
			}
		case "--include-css":
			c.includeCSS = true
		case "--ext":
			c.ext = shift()
			if c.ext == "" {
				return throwCLI("--ext <ext> required")
			}
		case "--disable-cache":
		case "--hoist-atrules":
			c.hoist = true
		case "-i", "--repl", "--interactive":
			fmt.Fprintln(os.Stderr, "Error: the REPL is not supported")
			return 1
		case "-I", "--include":
			p := shift()
			if p == "" {
				return throwCLI("--include <path> required")
			}
			c.paths = append(c.paths, p)
		case "-w", "--watch":
			fmt.Fprintln(os.Stderr, "Error: --watch is not supported")
			return 1
		case "-U", "--inline":
			c.urlFunction = true
		case "-u", "--use":
			p := shift()
			if p == "" {
				return throwCLI("--use <path> required")
			}
			if len(args) > 0 && args[0] == "--with" {
				args = args[1:]
				if len(args) == 0 {
					return throwCLI("--with <options> required")
				}
				args = args[1:]
			}
			if p == "url" {
				c.urlFunction = true
			} else {
				fmt.Fprintln(os.Stderr, "Error: plugins are not supported")
				return 1
			}
		case "--import":
			f := shift()
			if f == "" {
				return throwCLI("--import <file> required")
			}
			c.imports = append(c.imports, f)
		case "-r", "--resolve-url":
			c.resolveURL = true
		case "--resolve-url-nocheck":
			c.resolveURL = true
			c.resolveURLNocheck = true
		case "-D", "--deps":
			fmt.Fprintln(os.Stderr, "Error: --deps is not supported")
			return 1
		default:
			c.files = append(c.files, arg)
		}
	}

	if len(c.files) > 1 && isCSS(c.dest) {
		c.dest = filepath.Dir(c.dest)
	}

	if len(c.files) > 0 {
		return c.compileFiles(c.files)
	}
	return c.compileStdio()
}

func isCSS(file string) bool { return file != "" && filepath.Ext(file) == ".css" }

func (c *cli) options(filename string) *stylus.Options {
	cwd, _ := os.Getwd()
	return &stylus.Options{
		Filename:          filename,
		Compress:          c.compress,
		Firebug:           c.firebug,
		Linenos:           c.linenos,
		Paths:             append([]string{cwd}, c.paths...),
		Prefix:            c.prefix,
		Dest:              c.dest,
		HoistAtrules:      c.hoist,
		IncludeCSS:        c.includeCSS,
		Imports:           c.imports,
		ResolveURL:        c.resolveURL,
		ResolveURLNocheck: c.resolveURLNocheck,
		EnableURL:         c.urlFunction,
		URLLimit:          c.urlLimit,
	}
}

func (c *cli) compileStdio() int {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return throwCLI(err.Error())
	}
	str := string(data)
	css, rerr := stylus.NewRenderer(str, c.options("stdin")).Render()
	if rerr != nil {
		return reportError(rerr)
	}
	if c.compare {
		fmt.Fprintln(os.Stdout, "\n\x1b[1mInput:\x1b[0m")
		fmt.Fprintln(os.Stdout, str)
		fmt.Fprintln(os.Stdout, "\n\x1b[1mOutput:\x1b[0m")
	}
	fmt.Fprintln(os.Stdout, css)
	if c.compare {
		fmt.Fprintln(os.Stdout)
	}
	return 0
}

func (c *cli) compileFiles(files []string) int {
	for _, file := range files {
		if code := c.compileFile(file); code != 0 {
			return code
		}
	}
	return 0
}

func (c *cli) compileFile(file string) int {
	st, err := os.Stat(file)
	if err != nil {
		return reportSystemError("stat", file, err)
	}
	if st.IsDir() {
		entries, err := os.ReadDir(file)
		if err != nil {
			return reportSystemError("scandir", file, err)
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".styl") {
				names = append(names, filepath.Join(file, e.Name()))
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if code := c.compileFile(n); code != 0 {
				return code
			}
		}
		return 0
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return reportSystemError("open", file, err)
	}
	css, rerr := stylus.NewRenderer(string(data), c.options(file)).Render()
	if rerr != nil {
		return reportError(rerr)
	}
	return c.writeFile(file, css)
}

func (c *cli) createPath(file string) string {
	if len(c.files) == 1 && isCSS(c.dest) {
		return c.dest
	}
	base := filepath.Base(file)
	out := strings.TrimSuffix(base, filepath.Ext(base)) + c.ext
	if c.dest != "" {
		return filepath.Join(c.dest, out)
	}
	return filepath.Join(filepath.Dir(file), out)
}

func (c *cli) writeFile(file, css string) int {
	if c.print {
		io.WriteString(os.Stdout, css)
		return 0
	}
	p := c.createPath(file)
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		return throwCLI(err.Error())
	}
	if err := os.WriteFile(p, []byte(css), 0o666); err != nil {
		return throwCLI(err.Error())
	}
	fmt.Fprintf(os.Stdout, "  \x1b[90mcompiled\x1b[0m %s\n", p)
	return 0
}

func reportError(err *stylus.JSError) int {
	name := err.Name
	if err.FromStylus {
		name = "Error"
	}
	fmt.Fprint(os.Stderr, name+": "+err.Message+"\n")
	return 1
}

// reportSystemError prints what Node prints for an uncaught fs error.
func reportSystemError(syscall, path string, err error) int {
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: ENOENT: no such file or directory, %s '%s'\n", syscall, path)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
	return 1
}
