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

func TestTaskAdditionTimeoutCoversPreparationQueue(t *testing.T) {
	if got, err := workTimeout(Request{Action: actionTaskAdd}); err != nil || got != defaultTaskAddTimeout {
		t.Fatalf("default task addition timeout: %v %v", got, err)
	}
	if got, err := workTimeout(Request{Action: actionTaskAdd, Timeout: 45}); err != nil || got != 45*time.Second {
		t.Fatalf("custom task addition timeout: %v %v", got, err)
	}
	if _, err := workTimeout(Request{Action: actionTaskAdd, Timeout: 7201}); err == nil {
		t.Fatal("accepted an excessive task addition timeout")
	}
}
