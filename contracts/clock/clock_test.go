package clock

import (
	"testing"
	"time"
)

func TestFakeAdvanceFiresWaitersInOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	c := NewFake(t0)

	due := c.After(24 * time.Hour)       // HITL due_in
	half := c.After(12 * time.Hour)      // deputy handover
	grace := c.After(7 * 24 * time.Hour) // runtime offline grace

	c.Advance(12 * time.Hour)
	select {
	case ts := <-half:
		if !ts.Equal(t0.Add(12 * time.Hour)) {
			t.Fatalf("half fired at %v", ts)
		}
	default:
		t.Fatal("deputy handover did not fire at +12h")
	}
	select {
	case <-due:
		t.Fatal("due fired too early")
	default:
	}

	c.Advance(12 * time.Hour)
	select {
	case <-due:
	default:
		t.Fatal("due did not fire at +24h")
	}
	select {
	case <-grace:
		t.Fatal("grace fired too early")
	default:
	}

	c.Set(t0.Add(7 * 24 * time.Hour))
	select {
	case <-grace:
	default:
		t.Fatal("grace did not fire at +7d")
	}
	if got := c.Now(); !got.Equal(t0.Add(7 * 24 * time.Hour)) {
		t.Fatalf("now = %v", got)
	}
}

func TestFakeDoesNotGoBackwards(t *testing.T) {
	t0 := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	c := NewFake(t0)
	c.Advance(time.Hour)
	c.Set(t0)
	if !c.Now().Equal(t0.Add(time.Hour)) {
		t.Fatal("clock moved backwards")
	}
	c.Advance(-time.Hour)
	if !c.Now().Equal(t0.Add(time.Hour)) {
		t.Fatal("negative advance moved the clock")
	}
}

func TestRealSatisfiesInterface(t *testing.T) {
	var c Clock = Real{}
	if c.Since(c.Now()) < 0 {
		t.Fatal("negative since")
	}
}
