package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/sandbox"
)

// CapacityLimits bound work admitted by one supervisor. Zero resource budgets
// disable that particular budget; MaxTasks and QueueWait must be positive.
type CapacityLimits struct {
	MaxTasks int
	// MaxPreparations limits concurrent repository preparation and input staging.
	// Zero uses MaxTasks for callers that construct CapacityLimits directly.
	MaxPreparations int
	// MaxPendingPreparations bounds goroutines waiting to prepare input state.
	// Zero defaults to four times MaxPreparations.
	MaxPendingPreparations int
	MaxCPUs                int
	MaxMemoryMB            int
	QueueWait              time.Duration
}

func DefaultCapacityLimits() CapacityLimits {
	return CapacityLimits{MaxTasks: 2, MaxPreparations: 2, MaxPendingPreparations: 8, QueueWait: 15 * time.Minute}
}

type CapacityStatus struct {
	MaxTasks               int `json:"max_tasks"`
	MaxCPUs                int `json:"max_cpus"`
	MaxMemoryMB            int `json:"max_memory_mb"`
	ActiveTasks            int `json:"active_tasks"`
	ReservedCPUs           int `json:"reserved_cpus"`
	ReservedMemoryMB       int `json:"reserved_memory_mb"`
	WaitingTasks           int `json:"waiting_tasks"`
	MaxPreparations        int `json:"max_preparations"`
	MaxPendingPreparations int `json:"max_pending_preparations"`
	ActivePreparations     int `json:"active_preparations"`
	WaitingPreparations    int `json:"waiting_preparations"`
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
	mu                 sync.Mutex
	limits             CapacityLimits
	active             int
	cpus               int
	memory             int
	waiters            []*capacityWaiter
	parents            collections.Set[*capacityWaiter]
	preparations       chan struct{}
	preparationMu      sync.Mutex
	preparationWaiters int
}

func NewCapacity(limits CapacityLimits) (*Capacity, error) {
	if limits.MaxPreparations == 0 {
		limits.MaxPreparations = limits.MaxTasks
	}
	if limits.MaxPendingPreparations == 0 {
		limits.MaxPendingPreparations = limits.MaxPreparations * 4
	}
	if limits.MaxTasks < 1 || limits.MaxPreparations < 1 || limits.MaxPendingPreparations < 1 || limits.MaxCPUs < 0 || limits.MaxMemoryMB < 0 || limits.QueueWait <= 0 {
		return nil, fault.New(fault.CodeInvalidSpec, "invalid supervisor capacity limits")
	}
	return &Capacity{limits: limits, preparations: make(chan struct{}, limits.MaxPreparations)}, nil
}

func (c *Capacity) Status() CapacityStatus {
	c.preparationMu.Lock()
	waitingPreparations := c.preparationWaiters
	c.preparationMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return CapacityStatus{MaxTasks: c.limits.MaxTasks, MaxCPUs: c.limits.MaxCPUs,
		MaxMemoryMB: c.limits.MaxMemoryMB, ActiveTasks: c.active, ReservedCPUs: c.cpus,
		ReservedMemoryMB: c.memory, WaitingTasks: len(c.waiters),
		MaxPreparations: c.limits.MaxPreparations, MaxPendingPreparations: c.limits.MaxPendingPreparations,
		ActivePreparations:  len(c.preparations),
		WaitingPreparations: waitingPreparations}
}

// ReservePreparation bounds concurrent clone and input transfers as well as
// the number of requests waiting for a slot.
func (c *Capacity) ReservePreparation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	grant := func() func() {
		var once sync.Once
		return func() { once.Do(func() { <-c.preparations }) }
	}
	select {
	case c.preparations <- struct{}{}:
		return grant(), nil
	default:
	}
	c.preparationMu.Lock()
	if c.preparationWaiters >= c.limits.MaxPendingPreparations {
		c.preparationMu.Unlock()
		return nil, fault.New(fault.CodeCapacityExceeded, "supervisor preparation queue is full")
	}
	c.preparationWaiters++
	c.preparationMu.Unlock()
	defer func() {
		c.preparationMu.Lock()
		c.preparationWaiters--
		c.preparationMu.Unlock()
	}()
	waitCtx, cancel := context.WithTimeout(ctx, c.limits.QueueWait)
	defer cancel()
	select {
	case c.preparations <- struct{}{}:
		if err := waitCtx.Err(); err != nil {
			<-c.preparations
			return nil, fault.Wrap(fault.CodeCapacityWaitTimeout, "wait for preparation capacity", err, "preparation was not admitted")
		}
		return grant(), nil
	case <-waitCtx.Done():
		return nil, fault.Wrap(fault.CodeCapacityWaitTimeout, "wait for preparation capacity", waitCtx.Err(), "preparation was not admitted")
	}
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
			c.parents.Add(w)
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
	c.parents.Remove(w)
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
