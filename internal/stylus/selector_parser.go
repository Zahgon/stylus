/*!
 * Stylus - SelectorParser
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import "strings"

var selectorCombinators = []byte{'>', '+', '~'}

type selectorParser struct {
	str    string
	stack  []string
	parts  []string
	pos    int
	level  int
	nested bool
	ignore bool
	raw    bool
}

type selectorResult struct {
	val    string
	nested bool
}

func newSelectorParser(str string, stack, parts []string) *selectorParser {
	return &selectorParser{str: str, stack: stack, parts: parts, level: 2, nested: true}
}

func (s *selectorParser) skip(n int) {
	s.str = s.str[n:]
	s.pos += n
}

func (s *selectorParser) skipSpaces() {
	for len(s.str) > 0 && s.str[0] == ' ' {
		s.skip(1)
	}
}

func (s *selectorParser) advance() (string, bool) {
	if v, ok := s.root(); ok {
		return v, true
	}
	if v, ok := s.relative(false); ok {
		return v, true
	}
	if v, ok := s.initial(); ok {
		return v, true
	}
	if v, ok := s.escaped(); ok {
		return v, true
	}
	if v, ok := s.parent(); ok {
		return v, true
	}
	if v, ok := s.partial(); ok {
		return v, true
	}
	return s.char()
}

func (s *selectorParser) root() (string, bool) {
	if s.pos == 0 && len(s.str) > 0 && s.str[0] == '/' && sliceStr(s.str, 1, 5) != "deep" {
		s.nested = false
		s.skip(1)
		return "", true
	}
	return "", false
}

func sliceStr(s string, a, b int) string {
	if a > len(s) {
		return ""
	}
	if b > len(s) {
		b = len(s)
	}
	return s[a:b]
}

func (s *selectorParser) relative(multi bool) (string, bool) {
	if (s.pos == 0 || multi) && strings.HasPrefix(s.str, "../") {
		s.nested = false
		s.skip(3)
		for {
			if _, ok := s.relative(true); !ok {
				break
			}
			s.level++
		}
		if !s.raw {
			idx := len(s.stack) - s.level
			if idx >= 0 && idx < len(s.stack) && s.stack[idx] != "" {
				return s.stack[idx], true
			}
			s.ignore = true
		}
		return "", true
	}
	return "", false
}

func (s *selectorParser) initial() (string, bool) {
	if s.pos == 0 && len(s.str) > 1 && s.str[0] == '~' && s.str[1] == '/' {
		s.nested = false
		s.skip(2)
		if len(s.stack) > 0 {
			return s.stack[0], true
		}
		return "", true
	}
	return "", false
}

func (s *selectorParser) escaped() (string, bool) {
	if len(s.str) > 1 && s.str[0] == '\\' {
		c := s.str[1]
		if c == '&' || c == '^' {
			s.skip(2)
			return string(c), true
		}
	}
	return "", false
}

func (s *selectorParser) parent() (string, bool) {
	if len(s.str) > 0 && s.str[0] == '&' {
		s.nested = false
		if s.pos == 0 && (len(s.stack) == 0 || s.raw) {
			i := 1
			for i < len(s.str) && s.str[i] == ' ' {
				i++
			}
			if i < len(s.str) && indexOfByte(selectorCombinators, s.str[i]) {
				s.skip(i + 1)
				return "", true
			}
		}
		s.skip(1)
		if !s.raw && len(s.stack) > 0 {
			return s.stack[len(s.stack)-1], true
		}
		return "", true
	}
	return "", false
}

func indexOfByte(set []byte, c byte) bool {
	for _, x := range set {
		if x == c {
			return true
		}
	}
	return false
}

func (s *selectorParser) partial() (string, bool) {
	if len(s.str) > 1 && s.str[0] == '^' && s.str[1] == '[' {
		s.skip(2)
		s.skipSpaces()
		ret, _ := s.rangeSel()
		s.skipSpaces()
		if len(s.str) == 0 || s.str[0] != ']' {
			return "^[", true
		}
		s.nested = false
		s.skip(1)
		if ret != "" {
			return ret, true
		}
		s.ignore = true
		return "", true
	}
	return "", false
}

func (s *selectorParser) number() (float64, bool) {
	i := 0
	ret := ""
	if i < len(s.str) && s.str[i] == '-' {
		ret += string(s.str[i])
		i++
	}
	for i < len(s.str) && s.str[i] >= '0' && s.str[i] <= '9' {
		ret += string(s.str[i])
		i++
	}
	if ret != "" && ret != "-" {
		s.skip(i)
		return parseFloatJS(ret), true
	}
	if ret == "-" {
		s.skip(i)
		return 0, true // Number("-") is NaN in the original; it is unused here
	}
	return 0, false
}

func (s *selectorParser) rangeSel() (string, bool) {
	start, hasStart := s.number()
	ret := ""
	if strings.HasPrefix(s.str, "..") {
		s.skip(2)
		end, _ := s.number()
		length := float64(len(s.parts))
		if start < 0 {
			start = length + start - 1
		}
		if end < 0 {
			end = length + end - 1
		}
		if start > end {
			start, end = end, start
		}
		if end < length-1 {
			var parts []string
			a, b := int(start), int(end)+1
			if a < 0 {
				a = 0
			}
			if b > len(s.parts) {
				b = len(s.parts)
			}
			for _, part := range s.parts[a:b] {
				sel := newSelectorParser(part, s.stack, s.parts)
				sel.raw = true
				res := sel.parse()
				prefix := ""
				if res.nested {
					prefix = " "
				}
				parts = append(parts, prefix+res.val)
			}
			ret = strings.TrimSpace(strings.Join(parts, ""))
		}
	} else {
		idx := int(start)
		if start < 0 {
			idx = len(s.stack) + int(start) - 1
		}
		if hasStart && idx >= 0 && idx < len(s.stack) {
			ret = s.stack[idx]
		}
	}
	if ret != "" {
		return ret, true
	}
	s.ignore = true
	return "", false
}

func (s *selectorParser) char() (string, bool) {
	if len(s.str) == 0 {
		return "", false
	}
	c := s.str[0]
	s.skip(1)
	return string(c), true
}

func (s *selectorParser) parse() selectorResult {
	val := ""
	for len(s.str) > 0 {
		v, _ := s.advance()
		val += v
		if s.ignore {
			val = ""
			break
		}
	}
	return selectorResult{val: strings.TrimRight(val, " \t\n\r\v\f"), nested: s.nested}
}
