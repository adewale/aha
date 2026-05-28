package mcp

import (
	"bufio"
	"fmt"
	"io"
)

// Serve runs the MCP read loop against the given backend until input is
// closed. Frames are read from input; responses are written to output.
// Diagnostics, if any, go to stderr. The function returns nil on clean EOF
// and the first I/O or framing error otherwise.
func Serve(b Backend, input io.Reader, output io.Writer, stderr io.Writer) error {
	reader := bufio.NewReader(input)
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			msgs, rest, perr := ParseFrames(buf)
			buf = rest
			if perr != nil {
				fmt.Fprintln(stderr, "mcp: frame parse error:", perr)
				return perr
			}
			for _, msg := range msgs {
				resp, write := HandleMessage(b, msg)
				if !write {
					continue
				}
				frame, ferr := EncodeFrame(resp)
				if ferr != nil {
					fmt.Fprintln(stderr, "mcp: encode error:", ferr)
					return ferr
				}
				if _, werr := output.Write(frame); werr != nil {
					return werr
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
