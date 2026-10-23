/*!
 * Stylus - errors
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go. The original threw exceptions and caught a few
 * of them by name; the port panics with *JSError and recovers in the same
 * places, so the messages and the control flow stay the same.
 */

package stylus

import "fmt"

// JSError stands for the Error objects the original threw: `name` is the
// constructor name it used and `Message` is what the renderer prints.
type JSError struct {
	Name        string
	Message     string
	FromStylus  bool
	StylusStack string
	Lineno      int
	Column      int
	Filename    string
	Input       string
	HasPosition bool
}

func (e *JSError) Error() string { return e.Message }

func throwf(format string, args ...any) {
	panic(&JSError{Name: "Error", Message: fmt.Sprintf(format, args...)})
}

func throwNamed(name, format string, args ...any) {
	panic(&JSError{Name: name, Message: fmt.Sprintf(format, args...)})
}

// catchAny runs fn and returns the error it threw. A Go runtime fault is
// reported as a Stylus TypeError, which is what the original did with a
// JavaScript TypeError raised inside the compiler.
func catchAny(fn func()) (err *JSError) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*JSError); ok {
				err = e
				return
			}
			err = &JSError{Name: "TypeError", Message: fmt.Sprint(r)}
		}
	}()
	fn()
	return nil
}

// catch runs fn and returns the error it threw, if any.
func catch(fn func()) (err *JSError) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*JSError); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}
