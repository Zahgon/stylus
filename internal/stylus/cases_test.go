/*!
 * Stylus - integration cases
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from the original's test/run.js: every stylesheet under test/cases
 * is compiled and compared with the .css file beside it.
 */

package stylus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot locates the repository root from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

// TestCases compiles each stylesheet in test/cases with the options the
// original's runner picked from the file name and compares the result.
func TestCases(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "test", "cases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".styl") {
			continue
		}
		base := strings.TrimSuffix(name, ".styl")
		if base == "index" {
			continue
		}
		if base == "bifs.use" {
			// Plugins are out of scope for the port, so this case has no
			// counterpart here.
			continue
		}
		t.Run(base, func(t *testing.T) {
			styl, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read stylesheet: %v", err)
			}
			want, err := os.ReadFile(filepath.Join(dir, base+".css"))
			if err != nil {
				t.Skipf("no expected CSS beside %s", name)
			}
			opts := &Options{
				Filename: filepath.Join(dir, name),
				Paths: []string{root,
					filepath.Join(root, "test", "images"),
					filepath.Join(root, "test", "cases", "import.basic")},
			}
			if strings.Contains(base, "compress") {
				opts.Compress = true
			}
			if strings.Contains(base, "include") {
				opts.IncludeCSS = true
			}
			if strings.Contains(base, "prefix.") {
				opts.Prefix = "prefix-"
			}
			if strings.Contains(base, "hoist.") {
				opts.HoistAtrules = true
			}
			if strings.Contains(base, "resolver") {
				opts.ResolveURL = true
			}
			got, rerr := NewRenderer(string(styl), opts).Render()
			if rerr != nil {
				t.Fatalf("render %s: %s", name, rerr.Message)
			}
			if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
				t.Errorf("%s: compiled CSS differs from %s.css\n--- want ---\n%s\n--- got ---\n%s",
					name, base, firstLines(string(want), 20), firstLines(got, 20))
			}
		})
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}
