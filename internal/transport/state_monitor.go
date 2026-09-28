package transport

import (
	"context"
	"time"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
	"github.com/Covalane/turnyard/internal/stateops"
)

const stateScanInterval = 10 * time.Minute

func (s *Supervisor) monitorState(ctx context.Context, home string) {
	ticker := time.NewTicker(stateScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanState(ctx, home)
		}
	}
}

func (s *Supervisor) scanState(ctx context.Context, home string) {
	bytes, err := stateops.Usage(ctx, home)
	if err != nil {
		if ctx.Err() == nil {
			observe.LogFailure(ctx, "state usage scan failed", fault.Ensure(err, "scan state usage"))
		}
		return
	}
	s.stateBytes.Store(bytes)
	warning := s.stateWarnBytes > 0 && bytes >= s.stateWarnBytes
	previous := s.stateWarned.Swap(warning)
	if warning && !previous {
		observe.Log.WarnContext(ctx, "state usage exceeds warning threshold", "bytes", bytes, "thresholdBytes", s.stateWarnBytes)
	} else if previous && !warning {
		observe.Log.InfoContext(ctx, "state usage is below warning threshold", "bytes", bytes, "thresholdBytes", s.stateWarnBytes)
	}
}
