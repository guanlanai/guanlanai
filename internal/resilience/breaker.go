package resilience

import (
	"errors"
	"sync"
	"time"
)

// ErrOpen 表示熔断器已打开。
var ErrOpen = errors.New("circuit breaker open")

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// Breaker 是简单的连续失败熔断器。
type Breaker struct {
	mu sync.Mutex

	threshold int
	cooldown  time.Duration
	now       func() time.Time

	state            State
	failures         int
	openedAt         time.Time
	halfOpenInFlight bool
}

// New 创建熔断器。threshold 为连续失败次数，cooldown 为打开后的恢复等待时间。
func New(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Breaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
	}
}

// Allow 检查当前是否允许发起请求。
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	switch b.state {
	case StateClosed:
		return nil
	case StateOpen:
		if now.Sub(b.openedAt) < b.cooldown {
			return ErrOpen
		}
		b.state = StateHalfOpen
		b.halfOpenInFlight = false
		fallthrough
	case StateHalfOpen:
		if b.halfOpenInFlight {
			return ErrOpen
		}
		b.halfOpenInFlight = true
		return nil
	default:
		return ErrOpen
	}
}

// Record 记录一次请求结果；成功会关闭熔断器。
func (b *Breaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if success {
		b.state = StateClosed
		b.failures = 0
		b.halfOpenInFlight = false
		b.openedAt = time.Time{}
		return
	}

	b.halfOpenInFlight = false
	switch b.state {
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = b.now()
	case StateClosed:
		b.failures++
		if b.failures >= b.threshold {
			b.state = StateOpen
			b.openedAt = b.now()
		}
	}
}

// State 返回当前状态，主要用于测试和观测。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
