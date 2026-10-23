/*!
 * Stylus - Evaluator - built-in functions
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go.
 */

package stylus

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type builtin struct {
	fn     BuiltinFn
	params []string
	raw    bool
}

var builtins map[string]builtin

// arg returns the i-th argument or nil, as a missing JavaScript argument.
func arg(args []Node, i int) Node {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// present reports whether an argument was given and is not a null node, which
// is what `if (value)` tested in the original.
func present(n Node) bool {
	if n == nil {
		return false
	}
	if _, isNull := n.(*NullNode); isNull {
		return false
	}
	return true
}

// coerceValue is utils.coerce for the values the built-ins return.
func coerceString(s string) Node { return NewString(s, "") }

// jsRound is Math.round: halves go towards +Infinity.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	// Math.round gives -0 for every value in [-0.5, 0).
	if x < 0 && x >= -0.5 {
		return math.Copysign(0, -1)
	}
	return math.Floor(x + 0.5)
}

func toRGBA(n Node) *RGBANode {
	switch v := n.(type) {
	case *RGBANode:
		return v
	case *HSLANode:
		return v.RGBA()
	}
	return nil
}

func toHSLA(n Node) *HSLANode {
	switch v := n.(type) {
	case *RGBANode:
		return v.HSLA()
	case *HSLANode:
		return v
	}
	return nil
}

/*
 * Colour built-ins
 */

func bifAdjust(args []Node) Node {
	color := arg(args, 0)
	prop := arg(args, 1)
	amount := arg(args, 2)
	assertColor(color, "color")
	assertString(prop, "prop")
	assertType(amount, "unit", "amount")
	hsl := toHSLA(color).CloneNode(nil, nil).(*HSLANode)
	var field string
	switch stringOf(prop) {
	case "hue":
		field = "h"
	case "saturation":
		field = "s"
	case "lightness":
		field = "l"
	default:
		throwf("invalid adjustment property")
	}
	u := amount.(*UnitNode)
	val := u.Val
	cur := map[string]float64{"h": hsl.H, "s": hsl.S, "l": hsl.L}[field]
	if u.Type == "%" {
		if field == "l" && val > 0 {
			val = (100 - cur) * val / 100
		} else {
			val = cur * (val / 100)
		}
	}
	switch field {
	case "h":
		hsl.H += val
	case "s":
		hsl.S += val
	case "l":
		hsl.L += val
	}
	return hsl.RGBA()
}

func bifAlpha(ev *Evaluator, args []Node) Node {
	color := toRGBA(arg(args, 0))
	value := arg(args, 1)
	if color == nil {
		assertColor(arg(args, 0), "color")
	}
	if present(value) {
		return bifRGBA(ev, []Node{NewUnit(color.R, ""), NewUnit(color.G, ""), NewUnit(color.B, ""), value})
	}
	return NewUnit(color.A, "")
}

func bifBlend(ev *Evaluator, args []Node) Node {
	top := arg(args, 0)
	assertColor(top, "")
	t := toRGBA(top)
	var b *RGBANode
	if bottom := arg(args, 1); present(bottom) {
		assertColor(bottom, "")
		b = toRGBA(bottom)
	} else {
		b = NewRGBA(255, 255, 255, 1)
	}
	return NewRGBA(
		t.R*t.A+b.R*(1-t.A),
		t.G*t.A+b.G*(1-t.A),
		t.B*t.A+b.B*(1-t.A),
		t.A+b.A-t.A*b.A)
}

func channelSetter(idx int) BuiltinFn {
	return func(ev *Evaluator, args []Node) Node {
		color := toRGBA(arg(args, 0))
		if color == nil {
			assertColor(arg(args, 0), "color")
		}
		value := arg(args, 1)
		if present(value) {
			comps := []Node{NewUnit(color.R, ""), NewUnit(color.G, ""), NewUnit(color.B, ""), NewUnit(color.A, "")}
			comps[idx] = value
			return bifRGBA(ev, comps)
		}
		switch idx {
		case 0:
			return NewUnit(color.R, "")
		case 1:
			return NewUnit(color.G, "")
		}
		return NewUnit(color.B, "")
	}
}

var componentMap = map[string]string{
	"red": "r", "green": "g", "blue": "b", "alpha": "a",
	"hue": "h", "saturation": "s", "lightness": "l",
}
var componentUnit = map[string]string{"hue": "deg", "saturation": "%", "lightness": "%"}
var componentType = map[string]string{
	"red": "rgba", "blue": "rgba", "green": "rgba", "alpha": "rgba",
	"hue": "hsla", "saturation": "hsla", "lightness": "hsla",
}

func bifComponent(ev *Evaluator, args []Node) Node {
	color := arg(args, 0)
	name := arg(args, 1)
	assertColor(color, "color")
	assertString(name, "name")
	key := stringOf(name)
	unit := componentUnit[key]
	typ := componentType[key]
	field, ok := componentMap[key]
	if !ok {
		throwf("invalid color component \"%s\"", "undefined")
	}
	var val float64
	if typ == "rgba" {
		c := toRGBA(color)
		switch field {
		case "r":
			val = c.R
		case "g":
			val = c.G
		case "b":
			val = c.B
		case "a":
			val = c.A
		}
	} else {
		c := toHSLA(color)
		switch field {
		case "h":
			val = c.H
		case "s":
			val = c.S
		case "l":
			val = c.L
		}
	}
	return NewUnit(val, unit)
}

func bifLuminosity(ev *Evaluator, args []Node) Node {
	color := arg(args, 0)
	assertColor(color, "")
	c := toRGBA(color)
	proc := func(channel float64) float64 {
		channel = channel / 255
		if 0.03928 > channel {
			return channel / 12.92
		}
		return math.Pow((channel+0.055)/1.055, 2.4)
	}
	return NewUnit(0.2126*proc(c.R)+0.7152*proc(c.G)+0.0722*proc(c.B), "")
}

func bifContrast(ev *Evaluator, args []Node) Node {
	top := arg(args, 0)
	if top == nil || (top.NodeName() != "rgba" && top.NodeName() != "hsla") {
		inner := ""
		if top != nil {
			if _, isNull := top.(*NullNode); !isNull {
				inner = top.String()
			}
		}
		return NewLiteral("contrast(" + inner + ")")
	}
	result := NewObject()
	t := toRGBA(top)
	var b *RGBANode
	if bottom := arg(args, 1); present(bottom) {
		assertColor(bottom, "")
		b = toRGBA(bottom)
	} else {
		b = NewRGBA(255, 255, 255, 1)
	}
	lum := func(c *RGBANode) float64 {
		return bifLuminosity(ev, []Node{c}).(*UnitNode).Val
	}
	blendFn := func(x, y *RGBANode) *RGBANode {
		return bifBlend(ev, []Node{x, y}).(*RGBANode)
	}
	contrast := func(top, bottom *RGBANode) float64 {
		if 1 > top.A {
			top = blendFn(top, bottom)
		}
		l1 := lum(bottom) + 0.05
		l2 := lum(top) + 0.05
		ratio := l1 / l2
		if l2 > l1 {
			ratio = 1 / ratio
		}
		return jsRound(ratio*10) / 10
	}
	if 1 <= b.A {
		r := NewUnit(contrast(t, b), "")
		result.SetValue("ratio", r)
		result.SetValue("error", NewUnit(0, ""))
		result.SetValue("min", r)
		result.SetValue("max", r)
	} else {
		onBlack := contrast(t, blendFn(b, NewRGBA(0, 0, 0, 1)))
		onWhite := contrast(t, blendFn(b, NewRGBA(255, 255, 255, 1)))
		maxV := math.Max(onBlack, onWhite)
		proc := func(tc, bc float64) float64 {
			return math.Min(math.Max(0, (tc-bc*b.A)/(1-b.A)), 255)
		}
		closest := NewRGBA(proc(t.R, b.R), proc(t.G, b.G), proc(t.B, b.B), 1)
		minV := contrast(t, blendFn(b, closest))
		result.SetValue("ratio", NewUnit(jsRound((minV+maxV)*50)/100, ""))
		result.SetValue("error", NewUnit(jsRound((maxV-minV)*50)/100, ""))
		result.SetValue("min", NewUnit(minV, ""))
		result.SetValue("max", NewUnit(maxV, ""))
	}
	return result
}

func bifTransparentify(ev *Evaluator, args []Node) Node {
	top := arg(args, 0)
	assertColor(top, "")
	t := toRGBA(top)
	bottomArg := arg(args, 1)
	alphaArg := arg(args, 2)
	var b *RGBANode
	if !present(bottomArg) {
		b = NewRGBA(255, 255, 255, 1)
	} else if toRGBA(bottomArg) == nil {
		if !present(alphaArg) {
			alphaArg = bottomArg
		}
		b = NewRGBA(255, 255, 255, 1)
	} else {
		b = toRGBA(bottomArg)
	}
	assertColor(b, "")
	channels := []float64{
		(t.R - b.R) / (pick(0 < (t.R-b.R), 255, 0) - b.R),
		(t.G - b.G) / (pick(0 < (t.G-b.G), 255, 0) - b.G),
		(t.B - b.B) / (pick(0 < (t.B-b.B), 255, 0) - b.B),
	}
	sort.SliceStable(channels, func(i, j int) bool { return channels[i] > channels[j] })
	bestAlpha := channels[0]
	if present(alphaArg) {
		assertType(alphaArg, "unit", "alpha")
		a := alphaArg.(*UnitNode)
		if a.Type == "%" {
			bestAlpha = a.Val / 100
		} else if a.Type == "" {
			bestAlpha = a.Val
		}
	}
	bestAlpha = math.Max(math.Min(bestAlpha, 1), 0)
	proc := func(tc, bc float64) float64 {
		if bestAlpha == 0 {
			return bc
		}
		return bc + (tc-bc)/bestAlpha
	}
	return NewRGBA(proc(t.R, b.R), proc(t.G, b.G), proc(t.B, b.B), jsRound(bestAlpha*100)/100)
}

func pick(cond bool, a, b float64) float64 {
	if cond {
		return a
	}
	return b
}

func bifHSLA(ev *Evaluator, args []Node) Node {
	switch countArgs(args) {
	case 1:
		assertColor(arg(args, 0), "")
		return toHSLA(arg(args, 0))
	case 2:
		assertColor(arg(args, 0), "")
		color := toHSLA(arg(args, 0))
		assertType(arg(args, 1), "unit", "alpha")
		alpha := arg(args, 1).(*UnitNode).Clone()
		if alpha.Type == "%" {
			alpha.Val /= 100
		}
		return NewHSLA(color.H, color.S, color.L, alpha.Val)
	default:
		assertType(arg(args, 0), "unit", "hue")
		assertType(arg(args, 1), "unit", "saturation")
		assertType(arg(args, 2), "unit", "lightness")
		assertType(arg(args, 3), "unit", "alpha")
		alpha := arg(args, 3).(*UnitNode).Clone()
		if alpha.Type == "%" {
			alpha.Val /= 100
		}
		return NewHSLA(arg(args, 0).(*UnitNode).Val, arg(args, 1).(*UnitNode).Val,
			arg(args, 2).(*UnitNode).Val, alpha.Val)
	}
}

func countArgs(args []Node) int { return len(args) }

func bifHSL(ev *Evaluator, args []Node) Node {
	if countArgs(args) == 1 {
		assertColor(arg(args, 0), "color")
		return toHSLA(arg(args, 0))
	}
	return bifHSLA(ev, []Node{arg(args, 0), arg(args, 1), arg(args, 2), NewUnit(1, "")})
}

func bifRGBA(ev *Evaluator, args []Node) Node {
	switch countArgs(args) {
	case 1:
		assertColor(arg(args, 0), "")
		return toRGBA(arg(args, 0))
	case 2:
		assertColor(arg(args, 0), "")
		color := toRGBA(arg(args, 0))
		assertType(arg(args, 1), "unit", "alpha")
		alpha := arg(args, 1).(*UnitNode).Clone()
		if alpha.Type == "%" {
			alpha.Val /= 100
		}
		return NewRGBA(color.R, color.G, color.B, alpha.Val)
	default:
		assertType(arg(args, 0), "unit", "red")
		assertType(arg(args, 1), "unit", "green")
		assertType(arg(args, 2), "unit", "blue")
		assertType(arg(args, 3), "unit", "alpha")
		conv := func(n *UnitNode) float64 {
			if n.Type == "%" {
				return jsRound(n.Val * 2.55)
			}
			return n.Val
		}
		r := conv(arg(args, 0).(*UnitNode))
		g := conv(arg(args, 1).(*UnitNode))
		b := conv(arg(args, 2).(*UnitNode))
		alpha := arg(args, 3).(*UnitNode).Clone()
		if alpha.Type == "%" {
			alpha.Val /= 100
		}
		return NewRGBA(r, g, b, alpha.Val)
	}
}

func bifRGB(ev *Evaluator, args []Node) Node {
	if countArgs(args) == 1 {
		assertColor(arg(args, 0), "")
		color := toRGBA(arg(args, 0))
		return NewRGBA(color.R, color.G, color.B, 1)
	}
	return bifRGBA(ev, []Node{arg(args, 0), arg(args, 1), arg(args, 2), NewUnit(1, "")})
}

func hslaComponentSetter(which string) BuiltinFn {
	return func(ev *Evaluator, args []Node) Node {
		color := arg(args, 0)
		value := arg(args, 1)
		if present(value) {
			c := toHSLA(color)
			if c == nil {
				assertColor(color, "color")
			}
			comps := []Node{NewUnit(c.H, ""), NewUnit(c.S, ""), NewUnit(c.L, ""), NewUnit(c.A, "")}
			switch which {
			case "hue":
				comps[0] = value
			case "saturation":
				comps[1] = value
			case "lightness":
				comps[2] = value
			}
			return bifHSLA(ev, comps)
		}
		return bifComponent(ev, []Node{color, NewString(which, "")})
	}
}

/*
 * Trigonometry
 */

var angleFactors = map[string]float64{
	"rad":  1,
	"deg":  180 / math.Pi,
	"turn": 0.5 / math.Pi,
	"grad": 200 / math.Pi,
}

func convertAngle(value float64, unitName string) float64 {
	factor, ok := angleFactors[unitName]
	if !ok {
		return math.NaN()
	}
	return value * factor
}

// nodeToNumber is JavaScript's ToNumber applied to a node: the node becomes
// its string form, which is then read as a number.
func nodeToNumber(n Node) float64 {
	if n == nil {
		return math.NaN()
	}
	return jsNumberOfString(n.String())
}

// jsToInteger is ToIntegerOrInfinity over the values a node's `val` holds.
func jsToInteger(v any) float64 {
	var n float64
	switch t := v.(type) {
	case float64:
		n = t
	case string:
		n = jsNumberOfString(t)
	case bool:
		if t {
			n = 1
		}
	case Node:
		n = nodeToNumber(t)
	default:
		n = math.NaN()
	}
	if math.IsNaN(n) {
		return 0
	}
	return math.Trunc(n)
}

// jsNumberOfString is the global Number(): the whole string must be a numeric
// literal, and an empty string is zero.
func jsNumberOfString(s string) float64 {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0
	}
	if t == "Infinity" || t == "+Infinity" {
		return math.Inf(1)
	}
	if t == "-Infinity" {
		return math.Inf(-1)
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// outputUnit is the `output` parameter of the trigonometric built-ins. The
// original left it as the argument node, which is why the unit it prints
// keeps the argument's quotes.
func outputUnit(args []Node, i int) string {
	if a := arg(args, i); a != nil {
		return a.String()
	}
	return "deg"
}

func argNum(args []Node, i int) float64 {
	a := arg(args, i)
	if a == nil {
		return math.NaN()
	}
	if u, ok := a.(*UnitNode); ok {
		return u.Val
	}
	return toNumber(a.Hash())
}

// bifAsin reproduces the original's asin, which passed the argument node to
// Math.asin and the output node to the conversion table: the node is turned
// into a number, and a string's quotes are part of the table's key.
func bifAsin(ev *Evaluator, args []Node) Node {
	output := outputUnit(args, 1)
	m := math.Pow(10, 9)
	value := math.Asin(nodeToNumber(arg(args, 0)))
	return NewUnit(jsRound(convertAngle(value, output)*m)/m, output)
}

func bifAcos(ev *Evaluator, args []Node) Node {
	output := outputUnit(args, 1)
	asin := bifAsin(ev, args).(*UnitNode).Val
	m := math.Pow(10, 9)
	return NewUnit(jsRound((convertAngle(math.Pi/2, output)-asin)*m)/m, output)
}

func bifAtan(ev *Evaluator, args []Node) Node {
	output := outputUnit(args, 1)
	m := math.Pow(10, 9)
	value := math.Atan(nodeToNumber(arg(args, 0)))
	return NewUnit(jsRound(convertAngle(value, output)*m)/m, output)
}

func bifTan(ev *Evaluator, args []Node) Node {
	angle := arg(args, 0)
	assertType(angle, "unit", "angle")
	u := angle.(*UnitNode)
	radians := u.Val
	if u.Type == "deg" {
		radians *= math.Pi / 180
	}
	m := math.Pow(10, 9)
	sin := jsRound(math.Sin(radians)*m) / m
	cos := jsRound(math.Cos(radians)*m) / m
	tan := jsRound(m*sin/cos) / m
	return NewUnit(tan, "")
}

var mathFns = map[string]func(float64) float64{
	"abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor, "round": jsRound,
	"sin": math.Sin, "cos": math.Cos, "tan": math.Tan, "sqrt": math.Sqrt,
	"asin": math.Asin, "acos": math.Acos, "atan": math.Atan,
	"exp": math.Exp, "log": math.Log, "log2": math.Log2, "log10": math.Log10,
	"sign": func(x float64) float64 {
		if x > 0 {
			return 1
		}
		if x < 0 {
			return -1
		}
		return x
	},
	"trunc": math.Trunc, "cbrt": math.Cbrt, "sinh": math.Sinh, "cosh": math.Cosh,
	"tanh": math.Tanh, "fround": func(x float64) float64 { return float64(float32(x)) },
}

func bifMath(ev *Evaluator, args []Node) Node {
	n := arg(args, 0)
	fn := arg(args, 1)
	assertType(n, "unit", "n")
	assertString(fn, "fn")
	f, ok := mathFns[stringOf(fn)]
	if !ok {
		throwf("Math.%s is not a function", stringOf(fn))
	}
	return NewUnit(f(n.(*UnitNode).Val), n.(*UnitNode).Type)
}

var mathProps = map[string]float64{
	"PI": math.Pi, "E": math.E, "LN2": math.Ln2, "LN10": math.Log(10),
	"LOG2E": math.Log2E, "LOG10E": math.Log10E, "SQRT1_2": math.Sqrt(0.5),
	"SQRT2": math.Sqrt2,
}

func bifMathProp(ev *Evaluator, args []Node) Node {
	prop := arg(args, 0)
	v, ok := mathProps[stringOf(prop)]
	if !ok {
		return NewUnit(math.NaN(), "")
	}
	return NewUnit(v, "")
}

/*
 * Lists, strings and objects
 */

func bifLength(ev *Evaluator, args []Node) Node {
	expr := arg(args, 0)
	if expr == nil {
		return NewUnit(0, "")
	}
	if hasNodes(expr) {
		nodes := nodesOf(unwrap(expr))
		if len(nodes) == 1 && nodes[0].NodeName() == "object" {
			return NewUnit(float64(nodes[0].(*ObjectNode).Length()), "")
		}
		if len(nodes) == 1 && nodes[0].NodeName() == "string" {
			return NewUnit(float64(len([]rune(nodes[0].(*StringNode).Val))), "")
		}
		return NewUnit(float64(len(nodes)), "")
	}
	return NewUnit(1, "")
}

func bifListSeparator(ev *Evaluator, args []Node) Node {
	list := unwrap(arg(args, 0))
	isList := false
	switch v := list.(type) {
	case *ExpressionNode:
		isList = v.IsList
	case *ArgumentsNode:
		isList = v.IsList
	}
	if isList {
		return NewString(",", "")
	}
	return NewString(" ", "")
}

func exprOf(n Node) *ExpressionNode {
	switch v := n.(type) {
	case *ExpressionNode:
		return v
	case *ArgumentsNode:
		return &v.ExpressionNode
	}
	return nil
}

func bifPush(ev *Evaluator, args []Node) Node {
	expr := exprOf(unwrap(arg(args, 0)))
	if expr == nil {
		throwf("TypeError: push expects an expression")
	}
	for i := 1; i < len(args); i++ {
		expr.Nodes = append(expr.Nodes, unwrap(args[i]).CloneNode(nil, nil))
	}
	return NewUnit(float64(len(expr.Nodes)), "")
}

func bifUnshift(ev *Evaluator, args []Node) Node {
	expr := exprOf(unwrap(arg(args, 0)))
	if expr == nil {
		throwf("TypeError: unshift expects an expression")
	}
	for i := 1; i < len(args); i++ {
		expr.Nodes = append([]Node{unwrap(args[i])}, expr.Nodes...)
	}
	return NewUnit(float64(len(expr.Nodes)), "")
}

func bifPop(ev *Evaluator, args []Node) Node {
	expr := exprOf(unwrap(arg(args, 0)))
	if expr == nil || len(expr.Nodes) == 0 {
		return nil
	}
	last := expr.Nodes[len(expr.Nodes)-1]
	expr.Nodes = expr.Nodes[:len(expr.Nodes)-1]
	return last
}

func bifShift(ev *Evaluator, args []Node) Node {
	expr := exprOf(unwrap(arg(args, 0)))
	if expr == nil || len(expr.Nodes) == 0 {
		return nil
	}
	first := expr.Nodes[0]
	expr.Nodes = expr.Nodes[1:]
	return first
}

func bifRange(ev *Evaluator, args []Node) Node {
	start := arg(args, 0)
	stop := arg(args, 1)
	step := arg(args, 2)
	assertType(start, "unit", "start")
	assertType(stop, "unit", "stop")
	var stepVal float64 = 1
	if present(step) {
		assertType(step, "unit", "step")
		if step.(*UnitNode).Val == 0 {
			throwf("ArgumentError: \"step\" argument must not be zero")
		}
		stepVal = step.(*UnitNode).Val
	}
	list := NewExpression(false)
	for i := start.(*UnitNode).Val; i <= stop.(*UnitNode).Val; i += stepVal {
		list.Push(NewUnit(i, start.(*UnitNode).Type))
	}
	return list
}

func bifRemove(ev *Evaluator, args []Node) Node {
	object := arg(args, 0)
	key := arg(args, 1)
	assertType(object, "object", "object")
	assertString(key, "key")
	obj := object.(*ObjectNode)
	k := stringOf(key)
	if obj.Vals.has(k) {
		delete(obj.Vals.m, k)
		for i, o := range obj.Vals.order {
			if o == k {
				obj.Vals.order = append(obj.Vals.order[:i], obj.Vals.order[i+1:]...)
				break
			}
		}
	}
	return obj
}

func bifMerge(ev *Evaluator, args []Node) Node {
	dest := arg(args, 0)
	assertPresent(dest, "dest")
	d := unwrap(dest).First()
	assertType(d, "object", "dest")
	last := unwrap(args[len(args)-1]).First()
	deep := false
	if b, ok := last.(*BooleanNode); ok && b.Val {
		deep = true
	}
	end := len(args)
	if deep {
		end--
	}
	for i := 1; i < end; i++ {
		src := unwrap(args[i]).First()
		if s, ok := src.(*ObjectNode); ok {
			mergeObjects(d.(*ObjectNode), s, deep)
		}
	}
	return d
}

func mergeObjects(a, b *ObjectNode, deep bool) {
	for _, k := range b.Vals.keysInOrder() {
		bv, _ := b.Vals.get(k)
		if deep && a.Vals.has(k) {
			av, _ := a.Vals.get(k)
			nodeA := unwrap(av).First()
			nodeB := unwrap(bv).First()
			if oa, ok := nodeA.(*ObjectNode); ok {
				if ob, ok2 := nodeB.(*ObjectNode); ok2 {
					mergeObjects(oa, ob, deep)
					continue
				}
			}
		}
		a.Vals.set(k, bv)
	}
}

func bifOperate(ev *Evaluator, args []Node) Node {
	op := arg(args, 0)
	left := arg(args, 1)
	right := arg(args, 2)
	assertType(op, "string", "op")
	assertPresent(left, "left")
	assertPresent(right, "right")
	return left.Operate(op.(*StringNode).Val, right, nil)
}

func bifOppositePosition(ev *Evaluator, args []Node) Node {
	expr := NewExpression(false)
	for i, pos := range nodesOf(unwrap(arg(args, 0))) {
		assertString(pos, "position "+numToString(float64(i)))
		var out string
		switch stringOf(pos) {
		case "top":
			out = "bottom"
		case "bottom":
			out = "top"
		case "left":
			out = "right"
		case "right":
			out = "left"
		case "center":
			out = "center"
		default:
			throwf("invalid position %s", pos.String())
		}
		expr.Push(NewLiteral(out))
	}
	return expr
}

func bifType(ev *Evaluator, args []Node) Node {
	node := arg(args, 0)
	assertPresent(node, "expression")
	return NewString(node.NodeName(), "")
}

func bifUnit(ev *Evaluator, args []Node) Node {
	u := arg(args, 0)
	typ := arg(args, 1)
	assertType(u, "unit", "unit")
	if present(typ) {
		assertString(typ, "type")
		return NewUnit(u.(*UnitNode).Val, stringOf(typ))
	}
	return NewString(u.(*UnitNode).Type, "")
}

func bifUnquote(ev *Evaluator, args []Node) Node {
	s := arg(args, 0)
	assertString(s, "string")
	return NewLiteral(stringOf(s))
}

func bifConvert(ev *Evaluator, args []Node) Node {
	s := arg(args, 0)
	assertString(s, "str")
	return parseString(stringOf(s))
}

func bifSubstr(ev *Evaluator, args []Node) Node {
	val := arg(args, 0)
	start := arg(args, 1)
	length := arg(args, 2)
	assertString(val, "val")
	assertType(start, "unit", "start")
	runes := []rune(stringOf(val))
	s := int(start.(*UnitNode).Val)
	if s < 0 {
		s = len(runes) + s
		if s < 0 {
			s = 0
		}
	}
	if s > len(runes) {
		s = len(runes)
	}
	end := len(runes)
	// The original passed `length && length.val` to String#substr, so any
	// argument that is not a unit is read as a number through its own value.
	if length != nil {
		l := int(jsToInteger(length.JSVal()))
		if l < 0 {
			l = 0
		}
		if s+l < end {
			end = s + l
		}
	}
	res := string(runes[s:end])
	if _, isIdent := val.(*IdentNode); isIdent {
		return NewIdent(res, nil, false)
	}
	return NewString(res, "")
}

func jsSliceBounds(length int, start, end float64, hasStart, hasEnd bool) (int, int) {
	s := 0
	if hasStart {
		s = int(start)
		if s < 0 {
			s = length + s
		}
	}
	if s < 0 {
		s = 0
	}
	if s > length {
		s = length
	}
	e := length
	if hasEnd {
		e = int(end)
		if e < 0 {
			e = length + e
		}
	}
	if e < 0 {
		e = 0
	}
	if e > length {
		e = length
	}
	if e < s {
		e = s
	}
	return s, e
}

func bifSlice(ev *Evaluator, args []Node) Node {
	valArg := arg(args, 0)
	startArg := arg(args, 1)
	endArg := arg(args, 2)
	var start, end float64
	hasStart, hasEnd := false, false
	if present(startArg) {
		if sub := nodesOf(startArg); len(sub) > 0 {
			if u, ok := sub[0].(*UnitNode); ok {
				start, hasStart = u.Val, true
			}
		}
	}
	if present(endArg) {
		if sub := nodesOf(endArg); len(sub) > 0 {
			if u, ok := sub[0].(*UnitNode); ok {
				end, hasEnd = u.Val, true
			}
		}
	}
	val := nodesOf(unwrap(valArg))
	if len(val) > 1 {
		s, e := jsSliceBounds(len(val), start, end, hasStart, hasEnd)
		out := NewExpression(false)
		for _, n := range val[s:e] {
			out.Push(n)
		}
		return out
	}
	if len(val) == 0 {
		return NullNode_
	}
	runes := []rune(stringOf(val[0]))
	s, e := jsSliceBounds(len(runes), start, end, hasStart, hasEnd)
	result := string(runes[s:e])
	if _, isIdent := val[0].(*IdentNode); isIdent {
		return NewIdent(result, nil, false)
	}
	return NewString(result, "")
}

func bifSplit(ev *Evaluator, args []Node) Node {
	delim := arg(args, 0)
	val := arg(args, 1)
	assertString(delim, "delimiter")
	assertString(val, "val")
	parts := strings.Split(stringOf(val), stringOf(delim))
	expr := NewExpression(false)
	_, isIdent := val.(*IdentNode)
	for _, p := range parts {
		if isIdent {
			expr.Push(NewIdent(p, nil, false))
		} else {
			expr.Push(NewString(p, ""))
		}
	}
	return expr
}

func bifReplace(ev *Evaluator, args []Node) Node {
	pattern := arg(args, 0)
	replacement := arg(args, 1)
	val := arg(args, 2)
	assertString(pattern, "pattern")
	assertString(replacement, "replacement")
	assertString(val, "val")
	re, err := compileJSRegexp(stringOf(pattern), "")
	if err != nil {
		throwf("SyntaxError: Invalid regular expression: /%s/", stringOf(pattern))
	}
	res := re.ReplaceAllString(stringOf(val), expandJSReplacement(stringOf(replacement)))
	if _, isIdent := val.(*IdentNode); isIdent {
		return NewIdent(res, nil, false)
	}
	return NewString(res, "")
}

// expandJSReplacement turns JavaScript's $1 group references into Go's ${1}
// and escapes Go's own $ syntax elsewhere.
func expandJSReplacement(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) {
			j := i + 1
			if s[j] == '$' {
				b.WriteString("$$")
				i = j
				continue
			}
			k := j
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				k++
			}
			if k > j {
				b.WriteString("${" + s[j:k] + "}")
				i = k - 1
				continue
			}
			b.WriteString("$$")
			continue
		}
		if s[i] == '$' {
			b.WriteString("$$")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

const validRegexFlags = "igm"

func bifMatch(ev *Evaluator, args []Node) Node {
	pattern := arg(args, 0)
	val := arg(args, 1)
	flagsArg := arg(args, 2)
	assertType(pattern, "string", "pattern")
	assertString(val, "val")
	flags := ""
	if present(flagsArg) {
		f := stringOf(flagsArg)
		ok := f != ""
		for _, c := range f {
			if !strings.ContainsRune(validRegexFlags, c) {
				ok = false
			}
		}
		if ok {
			flags = f
		}
	}
	re, err := compileJSRegexp(stringOf(pattern), flags)
	if err != nil {
		throwf("SyntaxError: Invalid regular expression: /%s/", stringOf(pattern))
	}
	s := stringOf(val)
	if strings.Contains(flags, "g") {
		all := re.FindAllString(s, -1)
		if all == nil {
			return NullNode_
		}
		expr := NewExpression(false)
		for _, m := range all {
			expr.Push(NewString(m, ""))
		}
		return expr
	}
	m := re.FindStringSubmatch(s)
	if m == nil {
		return NullNode_
	}
	expr := NewExpression(false)
	for _, g := range m {
		expr.Push(NewString(g, ""))
	}
	return expr
}

// compileJSRegexp maps the JavaScript flags onto Go's inline flags.
func compileJSRegexp(pattern, flags string) (*regexp.Regexp, error) {
	prefix := ""
	if strings.Contains(flags, "i") {
		prefix += "i"
	}
	if strings.Contains(flags, "m") {
		prefix += "m"
	}
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	return regexp.Compile(pattern)
}

func bifBaseConvert(ev *Evaluator, args []Node) Node {
	num := arg(args, 0)
	base := arg(args, 1)
	width := arg(args, 2)
	assertPresent(num, "number")
	assertPresent(base, "base")
	n := firstUnitVal(num)
	b := firstUnitVal(base)
	w := 2.0
	if present(width) {
		if v := firstUnitVal(width); v != 0 {
			w = v
		}
	}
	result := toStringRadix(n, int(b))
	for float64(len(result)) < w {
		result = "0" + result
	}
	return NewLiteral(result)
}

func firstUnitVal(n Node) float64 {
	sub := nodesOf(unwrap(n))
	if len(sub) == 0 {
		return 0
	}
	if u, ok := sub[0].(*UnitNode); ok {
		return u.Val
	}
	return toNumber(sub[0].Hash())
}

func bifClone(ev *Evaluator, args []Node) Node {
	expr := arg(args, 0)
	assertPresent(expr, "expr")
	return expr.CloneNode(nil, nil)
}

func bifAddProperty(ev *Evaluator, args []Node) Node {
	name := arg(args, 0)
	expr := arg(args, 1)
	assertType(name, "expression", "name")
	first := unwrap(name).First()
	assertString(first, "name")
	assertType(expr, "expression", "expr")
	prop := NewProperty([]Node{first}, expr)
	block := ev.closestBlock()
	if block == nil {
		throwf("add-property() requires a block")
	}
	idx := block.Index
	if idx > len(block.Nodes) {
		idx = len(block.Nodes)
	}
	head := append([]Node{}, block.Nodes[:idx]...)
	tail := append([]Node{}, block.Nodes[idx:]...)
	block.Index++
	head = append(head, prop)
	block.Nodes = append(head, tail...)
	block.NodesGen++
	return prop
}

func bifDefine(ev *Evaluator, args []Node) Node {
	name := arg(args, 0)
	expr := arg(args, 1)
	global := arg(args, 2)
	assertType(name, "string", "name")
	e := unwrap(expr)
	scope := ev.currentScope()
	if present(global) && global.ToBoolean().Val {
		scope = ev.global.Scope()
	}
	scope.add(NewIdent(name.(*StringNode).Val, e, false))
	return NullNode_
}

func bifLookup(ev *Evaluator, args []Node) Node {
	name := arg(args, 0)
	assertType(name, "string", "name")
	node := ev.lookup(name.(*StringNode).Val)
	if node == nil {
		return NullNode_
	}
	return ev.visit(node)
}

func bifP(ev *Evaluator, args []Node) Node {
	for _, a := range args {
		expr := unwrap(a)
		if len(nodesOf(expr)) == 0 {
			continue
		}
		s := expr.String()
		s = strings.TrimPrefix(s, "(")
		s = strings.TrimSuffix(s, ")")
		fmt.Fprintf(stdout, "\u001b[90minspect:\u001b[0m %s\n", s)
	}
	return NullNode_
}

func bifWarn(ev *Evaluator, args []Node) Node {
	msg := arg(args, 0)
	assertType(msg, "string", "msg")
	fmt.Fprintf(stderr, "Warning: %s\n", msg.(*StringNode).Val)
	return NullNode_
}

func bifError(ev *Evaluator, args []Node) Node {
	msg := arg(args, 0)
	assertType(msg, "string", "msg")
	panic(&JSError{Name: "Error", Message: msg.(*StringNode).Val, FromStylus: true})
}

func bifTrace(ev *Evaluator, args []Node) Node {
	fmt.Fprintln(stdout, ev.stack.String())
	return NullNode_
}

func bifBasename(ev *Evaluator, args []Node) Node {
	p := arg(args, 0)
	ext := arg(args, 1)
	assertString(p, "path")
	base := path.Base(stringOf(p))
	if present(ext) {
		base = strings.TrimSuffix(base, stringOf(ext))
	}
	return NewString(base, "")
}

func bifDirname(ev *Evaluator, args []Node) Node {
	p := arg(args, 0)
	assertString(p, "path")
	return NewString(path.Dir(stringOf(p)), "")
}

func bifExtname(ev *Evaluator, args []Node) Node {
	p := arg(args, 0)
	assertString(p, "path")
	return NewString(path.Ext(stringOf(p)), "")
}

func bifPathjoin(ev *Evaluator, args []Node) Node {
	var parts []string
	for _, a := range args {
		parts = append(parts, stringOf(a.First()))
	}
	return NewString(path.Join(parts...), "")
}

func bifSprintf(args []Node) Node {
	fmtNode := arg(args, 0)
	first := nodesOf(unwrap(fmtNode))
	if len(first) == 0 {
		assertString(nil, "")
	}
	f := first[0]
	assertString(f, "")
	str := stringOf(f)
	i := 1
	var b strings.Builder
	for j := 0; j < len(str); j++ {
		if str[j] == '%' && j+1 < len(str) && (str[j+1] == 's' || str[j+1] == 'd') {
			spec := str[j+1]
			var a Node = NullNode_
			if i < len(args) && args[i] != nil {
				a = args[i]
			}
			i++
			if spec == 's' {
				b.WriteString(NewCompiler(a, sprintfOptions).Compile())
			} else {
				v := unwrap(a).First()
				u, ok := v.(*UnitNode)
				if !ok {
					throwf("%%d requires a unit")
				}
				b.WriteString(numToString(u.Val))
			}
			j++
			continue
		}
		b.WriteByte(str[j])
	}
	return NewLiteral(b.String())
}

var sprintfOptions = &Options{}

func bifSelector(ev *Evaluator, args []Node) Node {
	stack := ev.selectorStack()
	if len(args) == 1 {
		expr := unwrap(args[0])
		sub := nodesOf(expr)
		if len(sub) == 1 {
			assertString(expr.First(), "selector")
			val := stringOf(expr.First())
			parsed := newSelectorParser(val, nil, nil).parse().val
			if parsed == val {
				return NewString(val, "")
			}
			stack = append(stack, parseSelectorString(val))
		} else if len(sub) > 1 {
			isList := false
			if e, ok := expr.(*ExpressionNode); ok {
				isList = e.IsList
			}
			if a, ok := expr.(*ArgumentsNode); ok {
				isList = a.IsList
			}
			if isList {
				for _, sel := range sub {
					s := sel.First()
					assertString(s, "selector")
					stack = append(stack, parseSelectorString(stringOf(s)))
				}
			} else {
				var parts []string
				for _, node := range sub {
					assertString(node, "selector")
					parts = append(parts, stringOf(node))
				}
				stack = append(stack, parseSelectorString(strings.Join(parts, " ")))
			}
		}
	} else if len(args) > 1 {
		for _, sel := range args {
			s := sel.First()
			assertString(s, "selector")
			stack = append(stack, parseSelectorString(stringOf(s)))
		}
	}
	if len(stack) > 0 {
		return NewString(strings.Join(compileSelectors(stack, false, ""), ","), "")
	}
	return NewString("&", "")
}

func parseSelectorString(selector string) []*SelectorNode {
	parser := NewParser(selector, ParserOptions{})
	parser.pushState("selector-parts")
	node := parser.selector()
	var out []*SelectorNode
	for _, n := range nodesOf(node) {
		sel := n.(*SelectorNode)
		var b strings.Builder
		for _, seg := range sel.Segments {
			b.WriteString(seg.String())
		}
		sel.Val = b.String()
		out = append(out, sel)
	}
	return out
}

func bifSelectors(ev *Evaluator, args []Node) Node {
	stack := ev.selectorStack()
	expr := NewExpression(true)
	if len(stack) > 0 {
		for i, group := range stack {
			if len(group) > 1 {
				var parts []string
				for _, selector := range group {
					nested := newSelectorParser(selector.Val, nil, nil).parse().nested
					prefix := ""
					if nested && i > 0 {
						prefix = "& "
					}
					parts = append(parts, prefix+selector.Val)
				}
				expr.Push(NewString(strings.Join(parts, ","), ""))
			} else if len(group) == 1 {
				selector := group[0].Val
				nested := newSelectorParser(selector, nil, nil).parse().nested
				prefix := ""
				if nested && i > 0 {
					prefix = "& "
				}
				expr.Push(NewString(prefix+selector, ""))
			}
		}
	} else {
		expr.Push(NewString("&", ""))
	}
	return expr
}

func bifSelectorExists(ev *Evaluator, args []Node) Node {
	sel := arg(args, 0)
	assertString(sel, "selector")
	if ev.selectorsMap == nil {
		visitor := NewNormalizer(ev.root.CloneNode(nil, nil), nil)
		visitor.visit(visitor.root)
		ev.selectorsMap = visitor.selMap
	}
	_, ok := ev.selectorsMap[stringOf(sel)]
	return NewBoolean(ok)
}

func bifCurrentMedia(ev *Evaluator, args []Node) Node {
	block := ev.closestBlock()
	var lookForMedia func(node Node) string
	lookForMedia = func(node Node) string {
		if node == nil {
			return ""
		}
		if m, ok := node.(*MediaNode); ok {
			m.Val = ev.visit(m.Val)
			return m.String()
		}
		if b := blockOf(node); b != nil && b.Parent != nil && b.Parent.NodeOf != nil {
			return lookForMedia(b.Parent.NodeOf)
		}
		return ""
	}
	if block == nil {
		return NewString("", "")
	}
	return NewString(lookForMedia(block.NodeOf), "")
}

func bifPrefixClasses(ev *Evaluator, args []Node) Node {
	prefix := arg(args, 0)
	block := arg(args, 1)
	assertString(prefix, "prefix")
	assertType(block, "block", "block")
	old := ev.prefix
	ev.prefix = stringOf(prefix)
	ev.options.Prefix = ev.prefix
	res := ev.visit(block)
	ev.prefix = old
	ev.options.Prefix = old
	return res
}

func bifUse(ev *Evaluator, args []Node) Node {
	plugin := arg(args, 0)
	assertString(plugin, "plugin")
	name := stringOf(plugin)
	found, ok := lookup(name, ev.paths, "")
	if !ok {
		throwf("failed to locate plugin file \"%s\"", name)
	}
	throwf("plugin \"%s\" does not export a function", found)
	return nil
}

/*
 * JSON
 */

func bifJSON(ev *Evaluator, args []Node) Node {
	p := arg(args, 0)
	local := arg(args, 1)
	namePrefix := arg(args, 2)
	assertString(p, "path")
	pathStr := stringOf(p)
	found, ok := lookup(pathStr, ev.paths, ev.options.Filename)
	var options *ObjectNode
	if o, isObj := local.(*ObjectNode); isObj {
		options = o
	}
	if !ok {
		if options != nil && options.Get("optional").ToBoolean().Val {
			return NullNode_
		}
		throwf("failed to locate .json file %s", pathStr)
	}
	data, err := os.ReadFile(found)
	if err != nil {
		throwf("failed to locate .json file %s", pathStr)
	}
	parsed, perr := parseJSONOrdered(string(data))
	if perr != nil {
		throwf("%s", perr.Error())
	}
	if options != nil {
		leaveStrings := options.Get("leave-strings").ToBoolean().Val
		return jsonConvert(parsed, leaveStrings)
	}
	prefix := ""
	if present(namePrefix) {
		assertString(namePrefix, "namePrefix")
		prefix = stringOf(namePrefix)
	}
	scope := ev.global.Scope()
	if present(local) && local.ToBoolean().Val {
		scope = ev.currentScope()
	}
	var convert func(obj *jsonValue, p string)
	convert = func(obj *jsonValue, p string) {
		if p != "" {
			p = p + "-"
		}
		for _, k := range obj.keys {
			val := obj.values[k]
			name := p + k
			if val.isObject {
				convert(val, name)
			} else {
				node := jsonScalar(val)
				if s, isStr := node.(*StringNode); isStr {
					node = parseString(s.Val)
				}
				scope.add(NewIdent(prefix+name, node, false))
			}
		}
	}
	convert(parsed, "")
	return NullNode_
}

func jsonConvert(obj *jsonValue, leaveStrings bool) Node {
	ret := NewObject()
	for _, k := range obj.keys {
		val := obj.values[k]
		if val.isObject {
			ret.SetValue(k, jsonConvert(val, leaveStrings))
		} else {
			node := jsonScalar(val)
			if s, isStr := node.(*StringNode); isStr && !leaveStrings {
				node = parseString(s.Val)
			}
			ret.SetValue(k, node)
		}
	}
	return ret
}

func jsonScalar(v *jsonValue) Node {
	switch {
	case v.isNull:
		return NullNode_
	case v.isBool:
		return NewBoolean(v.boolean)
	case v.isNumber:
		return NewUnit(v.number, "")
	}
	return NewString(v.str, "")
}

/*
 * Images and urls
 */

var defaultMimes = map[string]string{
	".gif": "image/gif", ".png": "image/png", ".jpg": "image/jpeg",
	".jpeg": "image/jpeg", ".svg": "image/svg+xml", ".webp": "image/webp",
	".ttf": "application/x-font-ttf", ".eot": "application/vnd.ms-fontobject",
	".woff": "application/font-woff", ".woff2": "application/font-woff2",
}

func bifEmbedURL(ev *Evaluator, args []Node) Node {
	return urlFn(ev, args, nil, -1)
}

// urlFn is lib/functions/url.js; sizeLimit < 0 means no limit.
func urlFn(ev *Evaluator, args []Node, extraPaths []string, sizeLimit int) Node {
	urlNode := arg(args, 0)
	enc := arg(args, 1)
	compiler := NewCompiler(nil, nil)
	compiler.isURL = true
	var b strings.Builder
	for _, node := range nodesOf(urlNode) {
		b.WriteString(compiler.visit(node))
	}
	raw := b.String()
	pathname, search, hash := splitURL(raw)
	literal := NewLiteral("url(\"" + raw + "\")")
	ext := path.Ext(pathname)
	mime, ok := defaultMimes[ext]
	if !ok {
		return literal
	}
	if strings.Contains(pathname, ":") && !strings.HasPrefix(pathname, "/") {
		return literal
	}
	paths := append(append([]string{}, extraPaths...), ev.paths...)
	found, ok := lookup(pathname, paths, "")
	if !ok {
		return literal
	}
	data, err := os.ReadFile(found)
	if err != nil {
		return literal
	}
	if sizeLimit >= 0 && len(data) > sizeLimit {
		return literal
	}
	encoding := "base64"
	var result string
	if present(enc) && strings.ToLower(stringOf(enc.First())) == "utf8" {
		encoding = "charset=utf-8"
		s := regexp.MustCompile(`\s+`).ReplaceAllString(string(data), " ")
		s = regexp.MustCompile("[{}|\\\\^~\\[\\]`\"<>#%]").ReplaceAllStringFunc(s, func(m string) string {
			return "%" + strings.ToUpper(toStringRadix(float64(m[0]), 16))
		})
		result = strings.TrimSpace(s)
	} else {
		result = base64.StdEncoding.EncodeToString(data) + hash
	}
	_ = search
	return NewLiteral("url(\"data:" + mime + ";" + encoding + "," + result + "\")")
}

func splitURL(raw string) (pathname, search, hash string) {
	pathname = raw
	if i := strings.Index(pathname, "#"); i >= 0 {
		hash = pathname[i:]
		pathname = pathname[:i]
	}
	if i := strings.Index(pathname, "?"); i >= 0 {
		search = pathname[i:]
		pathname = pathname[:i]
	}
	return
}

func bifImageSize(ev *Evaluator, args []Node) Node {
	img := arg(args, 0)
	ignoreErr := arg(args, 1)
	assertType(img, "string", "img")
	found, ok := lookup(stringOf(img), ev.paths, "")
	if !ok {
		if present(ignoreErr) {
			expr := NewExpression(false)
			expr.Push(NewUnit(0, ""))
			expr.Push(NewUnit(0, ""))
			return expr
		}
		throwf("failed to locate file %s", stringOf(img))
	}
	image, err := OpenImage(found)
	if err == nil {
		var w, h int
		w, h, err = image.Size()
		if err == nil {
			expr := NewExpression(false)
			expr.Push(NewUnit(float64(w), "px"))
			expr.Push(NewUnit(float64(h), "px"))
			return expr
		}
	}
	if present(ignoreErr) {
		expr := NewExpression(false)
		expr.Push(NewUnit(0, ""))
		expr.Push(NewUnit(0, ""))
		return expr
	}
	throwf("%s", err.Error())
	return nil
}

func init() {
	raw := func(fn BuiltinFn) builtin { return builtin{fn: fn, raw: true} }
	p := func(fn BuiltinFn, params ...string) builtin { return builtin{fn: fn, params: params} }
	builtins = map[string]builtin{
		"add-property":      raw(bifAddProperty),
		"adjust":            p(func(ev *Evaluator, a []Node) Node { return bifAdjust(a) }, "color", "prop", "amount"),
		"alpha":             p(bifAlpha, "color", "value"),
		"base-convert":      raw(bifBaseConvert),
		"basename":          p(bifBasename, "p", "ext"),
		"blend":             p(bifBlend, "top", "bottom"),
		"blue":              p(channelSetter(2), "color", "value"),
		"clone":             raw(bifClone),
		"component":         p(bifComponent, "color", "name"),
		"contrast":          p(bifContrast, "top", "bottom"),
		"convert":           p(bifConvert, "str"),
		"current-media":     p(bifCurrentMedia),
		"define":            p(bifDefine, "name", "expr", "global"),
		"dirname":           p(bifDirname, "p"),
		"error":             p(bifError, "msg"),
		"extname":           p(bifExtname, "p"),
		"green":             p(channelSetter(1), "color", "value"),
		"hsl":               p(bifHSL, "hue", "saturation", "lightness"),
		"hsla":              p(bifHSLA, "hue", "saturation", "lightness", "alpha"),
		"hue":               p(hslaComponentSetter("hue"), "color", "value"),
		"image-size":        p(bifImageSize, "img", "ignoreErr"),
		"json":              p(bifJSON, "path", "local", "namePrefix"),
		"length":            raw(bifLength),
		"lightness":         p(hslaComponentSetter("lightness"), "color", "value"),
		"list-separator":    raw(bifListSeparator),
		"lookup":            p(bifLookup, "name"),
		"luminosity":        p(bifLuminosity, "color"),
		"match":             p(bifMatch, "pattern", "val", "flags"),
		"math":              p(bifMath, "n", "fn"),
		"merge":             raw(bifMerge),
		"extend":            raw(bifMerge),
		"operate":           p(bifOperate, "op", "left", "right"),
		"opposite-position": raw(bifOppositePosition),
		"p":                 raw(bifP),
		"pathjoin":          raw(bifPathjoin),
		"pop":               raw(bifPop),
		"push":              raw(bifPush),
		"append":            raw(bifPush),
		"range":             p(bifRange, "start", "stop", "step"),
		"red":               p(channelSetter(0), "color", "value"),
		"remove":            p(bifRemove, "object", "key"),
		"replace":           p(bifReplace, "pattern", "replacement", "val"),
		"rgb":               p(bifRGB, "red", "green", "blue"),
		"atan":              p(bifAtan, "trigValue", "output"),
		"asin":              p(bifAsin, "trigValue", "output"),
		"acos":              p(bifAcos, "trigValue", "output"),
		"rgba":              p(bifRGBA, "red", "green", "blue", "alpha"),
		"s":                 raw(func(ev *Evaluator, a []Node) Node { return bifSprintf(a) }),
		"saturation":        p(hslaComponentSetter("saturation"), "color", "value"),
		"selector-exists":   p(bifSelectorExists, "sel"),
		"selector":          raw(bifSelector),
		"selectors":         p(bifSelectors),
		"shift":             raw(bifShift),
		"split":             p(bifSplit, "delim", "val"),
		"substr":            p(bifSubstr, "val", "start", "length"),
		"slice":             raw(bifSlice),
		"tan":               p(bifTan, "angle"),
		"trace":             p(bifTrace),
		"transparentify":    p(bifTransparentify, "top", "bottom", "alpha"),
		"type":              p(bifType, "node"),
		"typeof":            p(bifType, "node"),
		"type-of":           p(bifType, "node"),
		"unit":              p(bifUnit, "unit", "type"),
		"unquote":           p(bifUnquote, "string"),
		"unshift":           raw(bifUnshift),
		"prepend":           raw(bifUnshift),
		"use":               p(bifUse, "plugin", "options"),
		"warn":              p(bifWarn, "msg"),
		"-math-prop":        p(bifMathProp, "prop"),
		"-prefix-classes":   p(bifPrefixClasses, "prefix", "block"),
	}
}
