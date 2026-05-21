package media_test

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/adewale/aha/internal/media"
)

func TestImageDetectionUsesExtensionAndMagicBytes(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	gif := []byte("GIF89arest")
	webp := []byte("RIFFxxxxWEBPrest")
	for _, tt := range []struct {
		name string
		path string
		data []byte
		want bool
	}{
		{"extension", "prompt.png", []byte("not-image"), true},
		{"png magic", "prompt", png, true},
		{"gif magic", "prompt", gif, true},
		{"webp magic", "prompt", webp, true},
		{"plain text", "prompt", []byte("hello"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := media.ReaderLooksImage(bufio.NewReader(bytes.NewReader(tt.data))) || func() bool { _, ok := media.ImageMIMEFromPath(tt.path); return ok }(); got != tt.want {
				t.Fatalf("image detection=%v want %v", got, tt.want)
			}
			if tt.want && len(tt.data) > 0 && tt.path == "prompt" {
				if mt, ok := media.ImageMIMEFromBytes(tt.data); !ok || mt == "" {
					t.Fatalf("magic MIME=(%q,%v), want image MIME", mt, ok)
				}
			}
		})
	}
}
