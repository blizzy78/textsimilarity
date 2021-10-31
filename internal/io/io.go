package io

import (
	"bufio"
	"bytes"
	"fmt"
)

// ReadLine reads a single line of text from r and returns it, using buf to do so.
// buf will be Reset before use, and may be reused across multiple calls to ReadLine.
func ReadLine(r *bufio.Reader, buf *bytes.Buffer) (string, error) {
	buf.Reset()

	for {
		data, prefix, err := r.ReadLine()
		if err != nil {
			return "", fmt.Errorf("read line: %w", err)
		}

		_, err = buf.Write(data)
		if err != nil {
			return "", fmt.Errorf("write to buffer: %w", err)
		}

		if prefix {
			continue
		}

		break
	}

	return buf.String(), nil
}
