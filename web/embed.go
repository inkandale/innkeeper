package web

import (
	"embed"
	"io/fs"
)

// dist holds the Vite build. It's empty, apart from .gitkeep, until
// npm run build has run.
//
//go:embed all:dist
var dist embed.FS

// Dist is the built UI.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
