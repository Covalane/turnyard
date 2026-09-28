package sandbox

import "testing"

func TestCapturedOutputIsBounded(t *testing.T) {
	var buffer cappedBuffer
	chunk := make([]byte, maxCapturedStreamBytes/2+1)
	if _, err := buffer.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if buffer.buffer.Len() != maxCapturedStreamBytes || !buffer.truncated {
		t.Fatalf("capture grew beyond limit: size=%d truncated=%t", buffer.buffer.Len(), buffer.truncated)
	}
}
