package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

func TestCapacityQueuesAndReleasesResources(t *testing.T) {
	c, err := NewCapacity(CapacityLimits{MaxTasks: 2, MaxCPUs: 2, MaxMemoryMB: 2048, QueueWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	spec := contracts.SandboxSpec{CPUs: 1, MemoryMB: 1024}
	releaseOne, err := c.Reserve(context.Background(), spec, CapacityRegular)
	if err != nil {
		t.Fatal(err)
	}
	releaseTwo, err := c.Reserve(context.Background(), spec, CapacityRegular)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		release, err := c.Reserve(context.Background(), spec, CapacityRegular)
		if err == nil {
			release()
		}
		result <- err
	}()
	deadline := time.After(time.Second)
	for c.Status().WaitingTasks != 1 {
		select {
		case <-deadline:
			t.Fatal("third task did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := c.Status(); got.ActiveTasks != 2 || got.ReservedCPUs != 2 || got.ReservedMemoryMB != 2048 {
		t.Fatalf("incorrect reservation: %+v", got)
	}
	releaseOne()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued task did not start")
	}
	releaseTwo()
	if got := c.Status(); got.ActiveTasks != 0 || got.WaitingTasks != 0 || got.ReservedMemoryMB != 0 {
		t.Fatalf("capacity was not released: %+v", got)
	}
}

func TestDelegatingTaskLeavesCapacityForChild(t *testing.T) {
	c, err := NewCapacity(CapacityLimits{MaxTasks: 2, MaxCPUs: 2, MaxMemoryMB: 2048, QueueWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	spec := contracts.SandboxSpec{CPUs: 1, MemoryMB: 1024}
	parentRelease, err := c.Reserve(context.Background(), spec, CapacityDelegating)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := make(chan error, 1)
	go func() {
		release, err := c.Reserve(context.Background(), spec, CapacityRegular)
		if err == nil {
			release()
		}
		ordinary <- err
	}()
	deadline := time.After(time.Second)
	for c.Status().WaitingTasks != 1 {
		select {
		case <-deadline:
			t.Fatal("ordinary task did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	childRelease, err := c.Reserve(context.Background(), spec, CapacityChild)
	if err != nil {
		t.Fatalf("child was blocked behind ordinary task: %v", err)
	}
	childRelease()
	parentRelease()
	select {
	case err := <-ordinary:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary task did not resume")
	}
}

func TestCapacityRejectsOversizeAndTimesOut(t *testing.T) {
	c, err := NewCapacity(CapacityLimits{MaxTasks: 1, MaxCPUs: 2, QueueWait: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	spec := contracts.SandboxSpec{CPUs: 2, MemoryMB: 512}
	if _, err := c.Reserve(context.Background(), spec, CapacityDelegating); fault.CodeOf(err) != fault.CodeCapacityExceeded {
		t.Fatalf("delegation without child slot was accepted: %v", err)
	}
	release, err := c.Reserve(context.Background(), spec, CapacityRegular)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Reserve(context.Background(), spec, CapacityRegular); fault.CodeOf(err) != fault.CodeCapacityWaitTimeout {
		t.Fatalf("queue did not time out: %v", err)
	}
	release()
	if got := c.Status(); got.ActiveTasks != 0 || got.WaitingTasks != 0 {
		t.Fatalf("reservation leaked after timeout: %+v", got)
	}
}
