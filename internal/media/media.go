package media

import (
	"bufio"
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

func MIMEFromPath(path string) string {
	return mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
}

func IsImageMIME(mt string) bool { return strings.HasPrefix(mt, "image/") }

func ExtFromMIME(mt string) string {
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func ImageMIMEFromPath(path string) (string, bool) {
	mt := MIMEFromPath(path)
	return mt, IsImageMIME(mt)
}

func ImageMIMEFromBytes(b []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif", true
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	default:
		return "", false
	}
}

func LooksImageBytes(b []byte) bool {
	_, ok := ImageMIMEFromBytes(b)
	return ok
}

func ReaderLooksImage(r *bufio.Reader) bool {
	b, err := r.Peek(16)
	if err != nil && len(b) == 0 {
		return false
	}
	return LooksImageBytes(b)
}

func FileLooksImage(path string) bool {
	if _, ok := ImageMIMEFromPath(path); ok {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [16]byte
	n, _ := io.ReadFull(f, head[:])
	return LooksImageBytes(head[:n])
}

func Dimensions(r io.Reader) (width, height int) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
