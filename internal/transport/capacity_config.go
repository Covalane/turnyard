package transport

import (
	"os"
	"strconv"
	"time"

	"github.com/Covalane/turnyard/internal/engine"
	"github.com/Covalane/turnyard/internal/fault"
)

// Capacity is a host setting, separate from the task's trusted Environment.
// A task cannot raise the limits of the supervisor that receives it.
func capacityFromEnvironment() (engine.CapacityLimits, error) {
	limits := engine.DefaultCapacityLimits()
	for _, item := range []struct {
		name   string
		target *int
	}{
		{"TURNYARD_MAX_ACTIVE_TASKS", &limits.MaxTasks},
		{"TURNYARD_MAX_ACTIVE_CPUS", &limits.MaxCPUs},
		{"TURNYARD_MAX_ACTIVE_MEMORY_MB", &limits.MaxMemoryMB},
	} {
		if raw, ok := os.LookupEnv(item.name); ok {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return limits, fault.Wrap(fault.CodeInvalidSpec, "parse supervisor capacity", err, "%s must be an integer", item.name)
			}
			*item.target = value
		}
	}
	if raw, ok := os.LookupEnv("TURNYARD_QUEUE_WAIT_SECONDS"); ok {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 1 || seconds > 7200 {
			return limits, fault.New(fault.CodeInvalidSpec, "TURNYARD_QUEUE_WAIT_SECONDS must be between 1 and 7200")
		}
		limits.QueueWait = time.Duration(seconds) * time.Second
	}
	if _, err := engine.NewCapacity(limits); err != nil {
		return limits, err
	}
	return limits, nil
}

func stateWarningBytes() (int64, error) {
	const defaultWarningMB = 10240
	value := defaultWarningMB
	if raw, ok := os.LookupEnv("TURNYARD_STATE_WARN_MB"); ok {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 1_000_000_000 {
			return 0, fault.New(fault.CodeInvalidSpec, "TURNYARD_STATE_WARN_MB must be a non-negative integer")
		}
		value = parsed
	}
	return int64(value) << 20, nil
}
