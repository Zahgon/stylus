/*!
 * Stylus - Image
 * Copyright (c) Automattic <developer.wordpress.com>
 * MIT Licensed
 *
 * Ported from JavaScript (lib/functions/image.js) to Go: the little image
 * reader behind the image-size() built-in, which reads the dimensions of a
 * GIF, PNG, JPEG or SVG file straight from its header.
 */

package stylus

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Image is one image file opened for its dimensions, as the original's Image
// class was.
type Image struct {
	Path    string
	data    []byte
	extname string
}

// OpenImage reads `path` and notes its extension, which is how the original
// recognised an SVG.
func OpenImage(path string) (*Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Image{Path: path, data: data, extname: strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))}, nil
}

// Type is the image kind, recognised from the first bytes as the original did.
func (im *Image) Type() string {
	b := im.data
	switch {
	case len(b) > 3 && b[0] == 0x47 && b[1] == 0x49 && b[2] == 0x46:
		return "gif"
	case len(b) > 3 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47:
		return "png"
	case len(b) > 1 && b[0] == 0xff && b[1] == 0xd8:
		return "jpeg"
	case im.extname == "svg":
		return "svg"
	}
	return ""
}

// Size returns the image's width and height.
func (im *Image) Size() (int, int, error) {
	b := im.data
	switch im.Type() {
	case "gif":
		if len(b) >= 10 {
			return int(binary.LittleEndian.Uint16(b[6:8])), int(binary.LittleEndian.Uint16(b[8:10])), nil
		}
	case "png":
		if len(b) >= 24 {
			return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), nil
		}
	case "jpeg":
		if w, h, ok := jpegSize(b); ok {
			return w, h, nil
		}
	case "svg":
		head := b
		if len(head) > 1024 {
			head = head[:1024]
		}
		if w, h, ok := svgSize(string(head)); ok {
			return w, h, nil
		}
	}
	return 0, 0, fmt.Errorf("failed to find width of %q", im.Path)
}

// jpegSize walks the JPEG segment headers to the frame header, as the
// original's loop over the file's blocks did.
func jpegSize(b []byte) (int, int, bool) {
	if len(b) < 6 {
		return 0, 0, false
	}
	offset := 4
	blockSize := int(b[offset])<<8 | int(b[offset+1])
	var width, height int
	for offset < len(b) {
		offset += blockSize
		if offset+1 >= len(b) || b[offset] != 0xff {
			break
		}
		// SOF0 or SOF2 (progressive)
		if b[offset+1] == 0xc0 || b[offset+1] == 0xc2 {
			if offset+8 >= len(b) {
				break
			}
			height = int(b[offset+5])<<8 | int(b[offset+6])
			width = int(b[offset+7])<<8 | int(b[offset+8])
		} else {
			offset += 2
			if offset+1 >= len(b) {
				break
			}
			blockSize = int(b[offset])<<8 | int(b[offset+1])
		}
	}
	return width, height, width != 0 || height != 0
}

var reSVGTag = regexp.MustCompile(`(?s)<svg\b([^>]*)>`)
var reSVGAttr = regexp.MustCompile(`(\w[\w-]*)\s*=\s*"([^"]*)"|(\w[\w-]*)\s*=\s*'([^']*)'`)

// svgSize reads the width and height attributes of the root element, which is
// what the original's SAX parse of the first kilobyte did.
func svgSize(s string) (int, int, bool) {
	m := reSVGTag.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	attrs := map[string]string{}
	for _, a := range reSVGAttr.FindAllStringSubmatch(m[1], -1) {
		if a[1] != "" {
			attrs[a[1]] = a[2]
		} else {
			attrs[a[3]] = a[4]
		}
	}
	w, okW := attrs["width"]
	h, okH := attrs["height"]
	if !okW || !okH {
		return 0, 0, false
	}
	wv := parseIntJS(w, 10)
	hv := parseIntJS(h, 10)
	if wv != wv || hv != hv {
		return 0, 0, false
	}
	return int(wv), int(hv), true
}
