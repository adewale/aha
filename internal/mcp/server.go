package mcp

import (
	"fmt"
	"io"
)

// Serve runs the MCP read loop against the given backend until input is
// closed. Frames are read from input; responses are written to output.
// Diagnostics, if any, go to stderr. The function returns nil on clean EOF
// and the first I/O or framing error otherwise.
//
// We read directly from input rather than wrapping it in bufio: the wire
// payload is a length-prefixed JSON blob, and ParseFrames buffers any partial
// tail internally. A bufio reader would add an opaque second buffer with no
// upside.
func Serve(b Backend, input io.Reader, output io.Writer, stderr io.Writer) error {
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		n, err := input.Read(chunk)
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
		// A conformant Reader returns (0, nil) only when it has nothing to
		// say *yet*; spinning would peg a CPU on a slow client. Wait for
		// the next byte before looping.
		if n == 0 {
			// Block on a 1-byte read so we can detect EOF cleanly without
			// burning cycles. The byte we read here is folded back into
			// the parse buffer on the next iteration.
			one := make([]byte, 1)
			m, rerr := input.Read(one)
			if m > 0 {
				buf = append(buf, one[:m]...)
			}
			if rerr == io.EOF {
				if len(buf) > 0 {
					if _, _, perr := ParseFrames(buf); perr != nil {
						return perr
					}
				}
				return nil
			}
			if rerr != nil {
				return rerr
			}
		}
	}
}
