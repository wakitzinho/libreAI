package provider

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SSEEvent is one Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

// SSEDecoder incrementally parses SSE from a reader. It enforces a maximum
// line length so a hostile or broken upstream cannot consume unbounded memory.
type SSEDecoder struct {
	reader  *bufio.Reader
	maxLine int
	pending *SSEEvent
	eof     bool
}

func NewSSEDecoder(r io.Reader, maxLine int) *SSEDecoder {
	if maxLine <= 0 {
		maxLine = 1 << 20
	}
	return &SSEDecoder{reader: bufio.NewReaderSize(r, 64<<10), maxLine: maxLine}
}

// Next returns the next event. It returns io.EOF only after any final
// unterminated event has been delivered.
func (d *SSEDecoder) Next() (SSEEvent, error) {
	if d.pending != nil {
		ev := *d.pending
		d.pending = nil
		return ev, nil
	}
	if d.eof {
		return SSEEvent{}, io.EOF
	}
	ev := SSEEvent{}
	var data []string
	dataLen := 0
	for {
		line, err := d.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				d.eof = true
				if len(data) > 0 {
					ev.Data = strings.Join(data, "\n")
					return ev, nil
				}
				return SSEEvent{}, io.EOF
			}
			return SSEEvent{}, err
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if len(data) == 0 {
				// Event name with no data is not dispatchable per the SSE spec.
				ev = SSEEvent{}
				continue
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			ev.Event = value
		case "data":
			dataLen += len(value) + 1
			if dataLen > d.maxLine {
				return SSEEvent{}, fmt.Errorf("SSE event data exceeds %d bytes", d.maxLine)
			}
			data = append(data, value)
		case "id":
			ev.ID = value
		case "retry":
			// Reconnection timing is not used by this single-request client.
		}
	}
}

func (d *SSEDecoder) readLine() (string, error) {
	var buf []byte
	for {
		frag, err := d.reader.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > d.maxLine {
			return "", fmt.Errorf("SSE line exceeds %d bytes", d.maxLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(buf) > 0 {
			return strings.TrimSuffix(string(buf), "\n"), nil
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(buf), "\n"), nil
	}
}
