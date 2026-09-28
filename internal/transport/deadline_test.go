package transport

import (
	"testing"
	"time"
)

func TestSessionCreationTimeout(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    time.Duration
		invalid bool
	}{
		{0, defaultSessionCreateTimeout, false},
		{45, 45 * time.Second, false},
		{-1, 0, true},
		{7201, 0, true},
	} {
		got, err := workTimeout(Request{Action: actionSessionCreate, Timeout: tc.seconds})
		if (err != nil) != tc.invalid || got != tc.want {
			t.Fatalf("timeout %d: got %v, err %v", tc.seconds, got, err)
		}
	}
	if got := wireTimeout(Request{Action: actionSessionCreate, Timeout: 45}); got != 45*time.Second+wireResponseMargin {
		t.Fatalf("wire timeout %v", got)
	}
}
