// Package webassets contains the production Angular application.
package webassets

import (
	"embed"
	"io/fs"
)

// The explicit index pattern makes a missing Angular build fail at compile time.
//
//go:embed root/index.html root
var content embed.FS

// Root returns the embedded build with root as its filesystem base.
func Root() fs.FS {
	root, err := fs.Sub(content, "root")
	if err != nil {
		panic(err)
	}
	return root
}
