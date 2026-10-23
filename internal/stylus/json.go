/*!
 * Stylus - JSON
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go: JSON.parse with the key order preserved, as
 * JavaScript objects keep it.
 */

package stylus

import (
	"encoding/json"
	"fmt"
	"strings"
)

type jsonValue struct {
	isObject bool
	isNull   bool
	isBool   bool
	isNumber bool
	keys     []string
	values   map[string]*jsonValue
	boolean  bool
	number   float64
	str      string
}

func parseJSONOrdered(s string) (*jsonValue, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("Unexpected token in JSON")
	}
	v, err := decodeJSONValue(dec, tok)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func decodeJSONValue(dec *json.Decoder, tok json.Token) (*jsonValue, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &jsonValue{isObject: true, values: map[string]*jsonValue{}}
			for {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := kt.(json.Delim); ok && d == '}' {
					return obj, nil
				}
				key := kt.(string)
				vt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				val, err := decodeJSONValue(dec, vt)
				if err != nil {
					return nil, err
				}
				if _, exists := obj.values[key]; !exists {
					obj.keys = append(obj.keys, key)
				}
				obj.values[key] = val
			}
		case '[':
			// Arrays enumerate as objects with numeric keys, as the original's
			// `for (var key in obj)` did.
			obj := &jsonValue{isObject: true, values: map[string]*jsonValue{}}
			i := 0
			for {
				vt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := vt.(json.Delim); ok && d == ']' {
					return obj, nil
				}
				val, err := decodeJSONValue(dec, vt)
				if err != nil {
					return nil, err
				}
				key := numToString(float64(i))
				obj.keys = append(obj.keys, key)
				obj.values[key] = val
				i++
			}
		}
	case string:
		return &jsonValue{str: t}, nil
	case bool:
		return &jsonValue{isBool: true, boolean: t}, nil
	case json.Number:
		return &jsonValue{isNumber: true, number: parseFloatJS(t.String())}, nil
	case nil:
		return &jsonValue{isNull: true}, nil
	}
	return nil, fmt.Errorf("Unexpected token in JSON")
}
