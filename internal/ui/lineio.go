package ui

import (
	"bytes"
	"io"
)

// lineReader delivers at most one line (up to and including '\n') per Read
// call.
//
// huh's accessible prompts each create a fresh bufio.Scanner over the form
// input; a scanner greedily buffers whatever a single Read returns. With a
// pipe feeding a whole answer script, the first prompt's scanner would
// swallow every remaining line, leaving later prompts at EOF. Serving one
// line per Read keeps sequential prompts in lockstep with the script (and
// behaves identically to a tty's line buffering).
type lineReader struct {
	src     io.Reader
	pending []byte
	srcEOF  bool // underlying reader exhausted
	sawEOF  bool // EOF has been handed to a caller at least once
}

func newLineReader(src io.Reader) *lineReader { return &lineReader{src: src} }

func (l *lineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if i := bytes.IndexByte(l.pending, '\n'); i >= 0 {
			n := min(len(p), i+1)
			copy(p, l.pending[:n])
			l.pending = l.pending[n:]
			return n, nil
		}
		if l.srcEOF {
			l.sawEOF = true
			if len(l.pending) == 0 {
				return 0, io.EOF
			}
			n := min(len(p), len(l.pending))
			copy(p, l.pending[:n])
			l.pending = l.pending[n:]
			return n, nil
		}
		chunk := make([]byte, 4096)
		n, err := l.src.Read(chunk)
		if n > 0 {
			l.pending = append(l.pending, chunk[:n]...)
		}
		if err != nil {
			l.srcEOF = true
		}
	}
}

// EOFSeen reports whether input has been exhausted, so drivers can stop
// instead of looping on defaulted prompts.
func (l *lineReader) EOFSeen() bool { return l.sawEOF }
