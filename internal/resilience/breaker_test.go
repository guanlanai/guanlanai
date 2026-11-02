package resilience

import (
	"errors"
	"testing"
	"time"
)

func TestBreakerOpensAndRecovers(t *testing.T) {
	now := time.Now()
	breaker := New(2, 10*time.Second)
	breaker.now = func() time.Time { return now }

	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	breaker.Record(false)
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	breaker.Record(false)
	if breaker.State() != StateOpen {
		t.Fatalf("state = %v, want open", breaker.State())
	}
	if err := breaker.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("Allow() error = %v, want ErrOpen", err)
	}

	now = now.Add(11 * time.Second)
	if err := breaker.Allow(); err != nil {
		t.Fatalf("half-open Allow() error = %v", err)
	}
	if err := breaker.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("second half-open Allow() error = %v, want ErrOpen", err)
	}
	breaker.Record(true)
	if breaker.State() != StateClosed {
		t.Fatalf("state = %v, want closed", breaker.State())
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	now := time.Now()
	breaker := New(1, time.Second)
	breaker.now = func() time.Time { return now }

	breaker.Record(false)
	now = now.Add(2 * time.Second)
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	breaker.Record(false)
	if breaker.State() != StateOpen {
		t.Fatalf("state = %v, want open", breaker.State())
	}
}
