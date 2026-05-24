package adapters

import (
	"io"
	"io/fs"
)

type ReadOnlySource interface {
	Open(name string) (io.ReadCloser, error)
	Stat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
}

type SourceOpener interface {
	Open(name string) (io.ReadCloser, error)
}
