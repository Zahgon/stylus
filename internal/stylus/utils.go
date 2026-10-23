/*!
 * Stylus - utils
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// isAbsolutePath is path.isAbsolute for the POSIX paths the port supports.
func isAbsolutePath(p string) bool {
	return strings.HasPrefix(p, "/")
}

// lookup finds `p` in `paths`, skipping `ignore`.
func lookup(p string, paths []string, ignore string) (string, bool) {
	if isAbsolutePath(p) {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		candidate := path.Join(paths[i], p)
		if ignore == candidate {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// find resolves a (possibly globbed) path against `paths`, sorted as the
// original sorted glob results.
func find(p string, paths []string, ignore string) []string {
	if isAbsolutePath(p) {
		if found := globSync(p); len(found) > 0 {
			sort.Strings(found)
			return found
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		candidate := path.Join(paths[i], p)
		if ignore == candidate {
			continue
		}
		if found := globSync(candidate); len(found) > 0 {
			sort.Strings(found)
			return found
		}
	}
	return nil
}

// globSync matches the glob patterns the original's `glob` dependency
// accepted, including `**` for any number of directories.
func globSync(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		if _, err := os.Stat(pattern); err == nil {
			return []string{pattern}
		}
		return nil
	}
	clean := path.Clean(pattern)
	rooted := strings.HasPrefix(clean, "/")
	segs := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	base := "."
	if rooted {
		base = "/"
	}
	// Leading literal segments are joined into the starting directory.
	i := 0
	for i < len(segs) && !strings.ContainsAny(segs[i], "*?[") {
		if base == "." {
			base = segs[i]
		} else if base == "/" {
			base = "/" + segs[i]
		} else {
			base = base + "/" + segs[i]
		}
		i++
	}
	if i == len(segs) {
		if _, err := os.Stat(base); err == nil {
			return []string{base}
		}
		return nil
	}
	var out []string
	globWalk(base, segs[i:], &out)
	return out
}

func globWalk(dir string, segs []string, out *[]string) {
	if len(segs) == 0 {
		*out = append(*out, dir)
		return
	}
	seg := segs[0]
	if seg == "**" {
		// Zero directories.
		globWalk(dir, segs[1:], out)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				globWalk(path.Join(dir, e.Name()), segs, out)
			}
		}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		ok, _ := path.Match(seg, e.Name())
		if !ok {
			continue
		}
		child := path.Join(dir, e.Name())
		if len(segs) == 1 {
			*out = append(*out, child)
		} else if e.IsDir() {
			globWalk(child, segs[1:], out)
		}
	}
}

// lookupIndex resolves `name` as a directory import: name/index.styl, then
// name/<basename>.styl, then a package under node_modules.
func lookupIndex(name string, paths []string, filename string) []string {
	found := find(path.Join(name, "index.styl"), paths, filename)
	if found == nil {
		base := path.Base(name)
		base = reStylExt.ReplaceAllString(base, "")
		found = find(path.Join(name, base+".styl"), paths, filename)
	}
	if found == nil && !strings.Contains(name, "node_modules") {
		found = lookupPackage(path.Join("node_modules", name), paths, filename)
	}
	return found
}

func lookupPackage(dir string, paths []string, filename string) []string {
	pkg, ok := lookup(path.Join(dir, "package.json"), paths, filename)
	if !ok {
		if reStylSuffix.MatchString(dir) {
			return lookupIndex(dir, paths, filename)
		}
		return lookupPackage(dir+".styl", paths, filename)
	}
	data, err := os.ReadFile(pkg)
	if err != nil {
		return lookupIndex(dir, paths, filename)
	}
	var manifest struct {
		Main string `json:"main"`
	}
	if json.Unmarshal(data, &manifest) == nil && manifest.Main != "" {
		return find(path.Join(dir, manifest.Main), paths, filename)
	}
	return lookupIndex(dir, paths, filename)
}

// formatException rewrites an error's message with the source excerpt and the
// caret the original printed.
func formatException(err *JSError, lineno, column int, filename, input string, context int) *JSError {
	if context == 0 {
		context = 8
	}
	half := context / 2
	lines := strings.Split("\n"+input, "\n")
	start := lineno - half
	if start < 1 {
		start = 1
	}
	end := lineno + half
	if end > len(lines) {
		end = len(lines)
	}
	pad := len(fmt.Sprint(end))
	var parts []string
	for i := start; i < end; i++ {
		curr := i
		line := lines[i]
		s := "   " + strings.Repeat(" ", pad-len(fmt.Sprint(curr))) + fmt.Sprint(curr) + "| " + line
		if curr == lineno {
			s += "\n" + strings.Repeat("-", len(fmt.Sprint(curr))+5+column-1) + "^"
		}
		parts = append(parts, s)
	}
	ctx := strings.Join(parts, "\n")
	err.Message = filename + ":" + fmt.Sprint(lineno) + ":" + fmt.Sprint(column) + "\n" + ctx + "\n\n" + err.Message + "\n"
	if err.StylusStack != "" {
		err.Message += err.StylusStack + "\n"
	}
	return err
}

func assertType(node Node, typ, param string) {
	assertPresent(node, param)
	if node.NodeName() == typ {
		return
	}
	msg := "expected "
	if param != "" {
		msg += "\"" + param + "\" to be a "
	}
	msg += typ + ", but got " + node.NodeName() + ":" + node.String()
	throwf("TypeError: %s", msg)
}

func assertString(node Node, param string) {
	assertPresent(node, param)
	switch node.NodeName() {
	case "string", "ident", "literal":
		return
	}
	throwf("TypeError: expected string, ident or literal, but got %s:%s", node.NodeName(), node.String())
}

func assertColor(node Node, param string) {
	assertPresent(node, param)
	switch node.NodeName() {
	case "rgba", "hsla":
		return
	}
	throwf("TypeError: expected rgba or hsla, but got %s:%s", node.NodeName(), node.String())
}

func assertPresent(node Node, name string) {
	if node != nil {
		return
	}
	if name != "" {
		throwf("\"%s\" argument required", name)
	}
	throwf("argument missing")
}

// unwrap peels single-node expressions, as the original's utils.unwrap did.
func unwrap(expr Node) Node {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ExpressionNode:
		if e.Preserve {
			return expr
		}
		if len(e.Nodes) != 1 {
			return expr
		}
		switch e.Nodes[0].(type) {
		case *ExpressionNode, *ArgumentsNode:
			return unwrap(e.Nodes[0])
		}
		return expr
	case *ArgumentsNode:
		if e.Preserve {
			return expr
		}
		if len(e.Nodes) != 1 {
			return expr
		}
		switch e.Nodes[0].(type) {
		case *ExpressionNode, *ArgumentsNode:
			return unwrap(e.Nodes[0])
		}
		return expr
	}
	return expr
}

func uniq(arr []string) []string {
	seen := map[string]bool{}
	var ret []string
	for _, s := range arr {
		if seen[s] {
			continue
		}
		seen[s] = true
		ret = append(ret, s)
	}
	return ret
}

// compileSelectors flattens the stack of selector groups into selector strings.
func compileSelectors(arr [][]*SelectorNode, leaveHidden bool, indent string) []string {
	var selectors []string
	var buf []string

	parse := func(selector *SelectorNode, buf []string) string {
		parts := []string{selector.Val}
		var parents []string
		str := newSelectorParser(parts[0], parents, parts).parse().val
		if len(buf) > 0 {
			for i := 0; i < len(buf); i++ {
				parts = append(parts, buf[i])
				parents = append(parents, str)
				child := newSelectorParser(buf[i], parents, parts).parse()
				if child.nested {
					str += " " + child.val
				} else {
					str = child.val
				}
			}
		}
		return strings.TrimSpace(str)
	}

	var compile func(i int)
	compile = func(i int) {
		if i > 0 {
			for _, selector := range arr[i] {
				if !leaveHidden && selector.IsPlaceholder() {
					continue
				}
				if selector.Inherits {
					buf = append([]string{selector.Val}, buf...)
					compile(i - 1)
					buf = buf[1:]
				} else {
					selectors = append(selectors, indent+parse(selector, buf))
				}
			}
		} else {
			for _, selector := range arr[0] {
				if !leaveHidden && selector.IsPlaceholder() {
					continue
				}
				str := parse(selector, buf)
				if str != "" {
					selectors = append(selectors, indent+str)
				}
			}
		}
	}

	compile(len(arr) - 1)
	return uniq(selectors)
}

// parseString parses `str` as an expression list, falling back to a literal.
func parseString(str string) Node {
	var ret Node
	err := catch(func() {
		p := NewParser(str, ParserOptions{})
		ret = p.List()
	})
	if err != nil {
		return NewLiteral(str)
	}
	return ret
}
