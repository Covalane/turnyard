package sandbox

import "bytes"

const maxCapturedStreamBytes = 16_000_000

// cappedBuffer drains noisy CLI output while retaining bounded memory.
type cappedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := maxCapturedStreamBytes - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(p[:min(len(p), remaining)])
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}
func (b *cappedBuffer) String() string {
	return b.buffer.String()
}
