/*!
 * Stylus - path helpers
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript to Go: the node:path calls the renderer makes.
 */

package stylus

import (
	"path"
	"path/filepath"
	"strings"
)

type strings_Builder = strings.Builder

func pathExt(p string) string { return path.Ext(p) }
func pathDir(p string) string { return path.Dir(p) }

func pathJoin(parts ...string) string { return path.Join(parts...) }

func pathRelative(from, to string) string {
	rel, err := filepath.Rel(from, to)
	if err != nil {
		return to
	}
	return rel
}
