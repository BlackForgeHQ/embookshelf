// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoots is returned when a path does not resolve inside
// any configured root. Serving and deleting share the rule, so a
// change to the sandbox cannot apply to one and miss the other.
var ErrPathOutsideRoots = errors.New("path outside allowed roots")

// SandboxPath resolves path and confirms it lands inside one of roots,
// returning the cleaned absolute path. Fails closed: an empty root list
// admits nothing. Comparison is on cleaned absolute paths with a
// separator-terminated prefix, so a traversal escape is rejected and
// `/data/lib` never admits `/data/lib-backup`.
//
// Lives here rather than in the HTTP layer because the delete side of
// the sandbox moved down with book deletion (LibraryService.DeleteBook);
// the serve side still gates on it from the handler. One implementation,
// two callers — the property the sandbox is documented to have.
func SandboxPath(path string, roots []string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("bad path")
	}

	sep := string(filepath.Separator)
	for _, root := range roots {
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == absRoot || strings.HasPrefix(abs, absRoot+sep) {
			return abs, nil
		}
	}
	return "", ErrPathOutsideRoots
}
