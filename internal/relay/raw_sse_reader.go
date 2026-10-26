package relay

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// splitRawSSEFrame preserves the wire bytes, including CRLF, event, id, retry,
// comments and multiline data. Parsing metadata never replaces the wire frame.
func splitRawSSEFrame(data []byte, atEOF bool) (int, []byte, error) {
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' && data[i] != '\n' {
			continue
		}
		end := i + 1
		if data[i] == '\r' {
			if end == len(data) && !atEOF {
				return 0, nil, nil
			}
			if end < len(data) && data[end] == '\n' {
				end++
			}
		}
		if i == start {
			return end, data[:end], nil
		}
		start = end
		i = end - 1
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
func rawSSEData(frame []byte) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(string(frame), "\r\n", "\n"), "\r", "\n")
	var lines []string
	for _, line := range strings.Split(normalized, "\n") {
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimPrefix(line, "data:")
			line = strings.TrimPrefix(line, " ")
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
func rawSSEScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	scanner.Split(splitRawSSEFrame)
	scanner.Buffer(make([]byte, 4096), maxSSEEventSize)
	return scanner
}
func rawSSEHasData(frame []byte) bool {
	for _, line := range bytes.Split(bytes.ReplaceAll(frame, []byte("\r"), []byte("\n")), []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			return true
		}
	}
	return false
}
