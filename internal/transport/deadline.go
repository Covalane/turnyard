package transport

import (
	"time"

	"github.com/Covalane/turnyard/internal/fault"
)

const (
	defaultSessionCreateTimeout = 10 * time.Minute
	maxSessionCreateTimeout     = 2 * time.Hour
	wireResponseMargin          = 5 * time.Second
)

// workTimeout keeps slow repository preparation separate from ordinary RPCs.
func workTimeout(req Request) (time.Duration, error) {
	if req.Action == actionSessionPublish {
		if req.Timeout < 1 || req.Timeout > 7200 {
			return 0, fault.New(fault.CodeInvalidRequest, "session publish timeout must be between 1 and 7200 seconds")
		}
		return time.Duration(req.Timeout) * time.Second, nil
	}
	if req.Action != actionSessionCreate {
		return supervisorWorkTimeout, nil
	}
	if req.Timeout == 0 {
		return defaultSessionCreateTimeout, nil
	}
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout < time.Second || timeout > maxSessionCreateTimeout {
		return 0, fault.New(fault.CodeInvalidRequest, "session create timeout must be between 1 and 7200 seconds")
	}
	return timeout, nil
}

func wireTimeout(req Request) time.Duration {
	work, err := workTimeout(req)
	if err != nil {
		return supervisorWireTimeout
	}
	return work + wireResponseMargin
}
