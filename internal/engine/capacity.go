package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

// CapacityLimits bound work admitted by one supervisor. Zero resource budgets
// disable that particular budget; MaxTasks and QueueWait must be positive.
type CapacityLimits struct {
	MaxTasks    int
	MaxCPUs     int
	MaxMemoryMB int
	QueueWait   time.Duration
}

func DefaultCapacityLimits() CapacityLimits {
	return CapacityLimits{MaxTasks: 2, QueueWait: 15 * time.Minute}
}

type CapacityStatus struct {
	MaxTasks         int `json:"maxTasks"`
	MaxCPUs          int `json:"maxCPUs"`
	MaxMemoryMB      int `json:"maxMemoryMB"`
	ActiveTasks      int `json:"activeTasks"`
	ReservedCPUs     int `json:"reservedCPUs"`
	ReservedMemoryMB int `json:"reservedMemoryMB"`
	WaitingTasks     int `json:"waitingTasks"`
}

type capacityWaiter struct {
	cpus, memory int
	kind         CapacityKind
	ready        chan struct{}
	granted      bool
}

type CapacityKind uint8

const (
	CapacityRegular CapacityKind = iota
	CapacityDelegating
	CapacityChild
)

type Capacity struct {
	mu      sync.Mutex
	limits  CapacityLimits
	active  int
	cpus    int
	memory  int
	waiters []*capacityWaiter
	parents map[*capacityWaiter]bool
}

func NewCapacity(limits CapacityLimits) (*Capacity, error) {
	if limits.MaxTasks < 1 || limits.MaxCPUs < 0 || limits.MaxMemoryMB < 0 || limits.QueueWait <= 0 {
		return nil, fault.New(fault.CodeInvalidSpec, "invalid supervisor capacity limits")
	}
	return &Capacity{limits: limits, parents: map[*capacityWaiter]bool{}}, nil
}

func (c *Capacity) Status() CapacityStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CapacityStatus{MaxTasks: c.limits.MaxTasks, MaxCPUs: c.limits.MaxCPUs,
		MaxMemoryMB: c.limits.MaxMemoryMB, ActiveTasks: c.active, ReservedCPUs: c.cpus,
		ReservedMemoryMB: c.memory, WaitingTasks: len(c.waiters)}
}

// Reserve uses FIFO admission. The timeout bounds queueing, while the caller's
// task timeout continues to bound the actual agent or check invocation.
func (c *Capacity) Reserve(ctx context.Context, spec contracts.SandboxSpec, kind CapacityKind) (func(), error) {
	cpus, memory := sandbox.EffectiveResources(spec)
	if c.limits.MaxCPUs > 0 && cpus > c.limits.MaxCPUs || c.limits.MaxMemoryMB > 0 && memory > c.limits.MaxMemoryMB {
		return nil, fault.New(fault.CodeCapacityExceeded, "task sandbox request exceeds supervisor capacity")
	}
	if kind == CapacityDelegating && (c.limits.MaxTasks < 2 ||
		c.limits.MaxCPUs > 0 && cpus*2 > c.limits.MaxCPUs ||
		c.limits.MaxMemoryMB > 0 && memory*2 > c.limits.MaxMemoryMB) {
		return nil, fault.New(fault.CodeCapacityExceeded, "delegating task requires capacity for its child")
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.limits.QueueWait)
	defer cancel()
	w := &capacityWaiter{cpus: cpus, memory: memory, kind: kind, ready: make(chan struct{})}
	c.mu.Lock()
	c.waiters = append(c.waiters, w)
	c.dispatch()
	c.mu.Unlock()
	select {
	case <-w.ready:
		return c.releaseFunc(w), nil
	case <-waitCtx.Done():
		c.mu.Lock()
		if w.granted {
			c.unreserve(w)
		} else {
			for i, pending := range c.waiters {
				if pending == w {
					c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
					break
				}
			}
		}
		c.dispatch()
		c.mu.Unlock()
		return nil, fault.Wrap(fault.CodeCapacityWaitTimeout, "wait for supervisor capacity", waitCtx.Err(), "task was not admitted")
	}
}

// dispatch is called with mu held. A child can pass blocked ordinary work so
// a parent does not occupy the last slot while waiting for that child.
func (c *Capacity) dispatch() {
	for len(c.waiters) > 0 {
		index := 0
		if !c.canAdmit(c.waiters[index]) {
			index = -1
			for i, w := range c.waiters {
				if w.kind == CapacityChild && c.canAdmit(w) {
					index = i
					break
				}
			}
			if index < 0 {
				return
			}
		}
		w := c.waiters[index]
		c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
		c.active++
		c.cpus += w.cpus
		c.memory += w.memory
		if w.kind == CapacityDelegating {
			c.parents[w] = true
		}
		w.granted = true
		close(w.ready)
	}
}

func (c *Capacity) canAdmit(w *capacityWaiter) bool {
	if c.active >= c.limits.MaxTasks {
		return false
	}
	if w.kind != CapacityChild && (len(c.parents) > 0 || w.kind == CapacityDelegating) && c.active+1 >= c.limits.MaxTasks {
		return false
	}
	reserveCPUs, reserveMemory := 0, 0
	if w.kind != CapacityChild {
		for parent := range c.parents {
			reserveCPUs = max(reserveCPUs, parent.cpus)
			reserveMemory = max(reserveMemory, parent.memory)
		}
		if w.kind == CapacityDelegating {
			reserveCPUs = max(reserveCPUs, w.cpus)
			reserveMemory = max(reserveMemory, w.memory)
		}
	}
	return (c.limits.MaxCPUs == 0 || c.cpus+w.cpus+reserveCPUs <= c.limits.MaxCPUs) &&
		(c.limits.MaxMemoryMB == 0 || c.memory+w.memory+reserveMemory <= c.limits.MaxMemoryMB)
}

func (c *Capacity) unreserve(w *capacityWaiter) {
	c.active--
	c.cpus -= w.cpus
	c.memory -= w.memory
	delete(c.parents, w)
}

func (c *Capacity) releaseFunc(w *capacityWaiter) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.unreserve(w)
			c.dispatch()
			c.mu.Unlock()
		})
	}
}
