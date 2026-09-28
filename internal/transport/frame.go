package transport

import (
	"bufio"
	"errors"
	"io"
	"time"
)

const (
	maxRequestBytes       = 2_000_000
	maxResponseBytes      = 32_000_000
	supervisorWorkTimeout = 30 * time.Second
	supervisorWireTimeout = 35 * time.Second
)

var errFrameTooLarge = errors.New("supervisor message exceeds size limit")

// readFrame bounds memory use while allowing result payloads larger than requests.
func readFrame(r io.Reader, maxBytes int64) ([]byte, error) {
	line, err := bufio.NewReader(io.LimitReader(r, maxBytes+1)).ReadBytes('\n')
	if int64(len(line)) > maxBytes {
		return nil, errFrameTooLarge
	}
	return line, err
}
