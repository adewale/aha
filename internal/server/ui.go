package server

import (
	"embed"
	"io/fs"
)

//go:embed ui
var uiRaw embed.FS

func staticFS() fs.FS {
	sub, err := fs.Sub(uiRaw, "ui")
	if err != nil {
		// embed.FS is built at compile time; this can't fail at runtime
		// unless the layout changes underneath us.
		panic(err)
	}
	return sub
}

func indexHTML() ([]byte, error) {
	return fs.ReadFile(staticFS(), "index.html")
}
