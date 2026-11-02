package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/guanlanai/guanlanai/internal/logging"
)

// Handler 处理单个 Telegram update。
type Handler func(ctx context.Context, update *models.Update)

// Dispatcher 保证同一个 chat 的 update 串行处理，并限制全局并发。
type Dispatcher struct {
	ctx     context.Context
	handler Handler
	logger  *slog.Logger
	sem     chan struct{}
	idle    time.Duration

	mu     sync.Mutex
	queues map[int64]*chatQueue
	wg     sync.WaitGroup
}

type chatQueue struct {
	ch         chan *models.Update
	lastActive atomic.Int64
	processing atomic.Bool
}

// NewDispatcher 创建分发器；maxConcurrency 是全局同时处理 update 的上限。
func NewDispatcher(ctx context.Context, handler Handler, maxConcurrency int, logger *slog.Logger) *Dispatcher {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	d := &Dispatcher{
		ctx:     ctx,
		handler: handler,
		logger:  logger,
		sem:     make(chan struct{}, maxConcurrency),
		idle:    10 * time.Minute,
		queues:  make(map[int64]*chatQueue),
	}
	go d.janitor()
	return d
}

// Enqueue 将 update 放入对应 chat 的队列；队列满时返回 false。
func (d *Dispatcher) Enqueue(chatID int64, update *models.Update) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	q := d.queues[chatID]
	if q == nil {
		q = &chatQueue{ch: make(chan *models.Update, 2)}
		q.lastActive.Store(time.Now().UnixNano())
		d.queues[chatID] = q
		d.wg.Add(1)
		go d.run(chatID, q)
	}

	select {
	case q.ch <- update:
		q.lastActive.Store(time.Now().UnixNano())
		return true
	default:
		d.logger.Warn("chat 队列已满", "chat", logging.HashID(chatID))
		return false
	}
}

// Wait 等待所有 chat worker 退出，用于优雅关闭。
func (d *Dispatcher) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待 Telegram 任务退出: %w", ctx.Err())
	}
}

func (d *Dispatcher) run(chatID int64, q *chatQueue) {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case update, ok := <-q.ch:
			if !ok {
				return
			}
			if !d.process(q, update) {
				return
			}
		}
	}
}

func (d *Dispatcher) process(q *chatQueue, update *models.Update) bool {
	q.processing.Store(true)
	defer q.processing.Store(false)
	defer q.lastActive.Store(time.Now().UnixNano())

	select {
	case d.sem <- struct{}{}:
	case <-d.ctx.Done():
		return false
	}
	defer func() { <-d.sem }()

	defer func() {
		if recovered := recover(); recovered != nil {
			d.logger.Error("处理 Telegram update 时 panic", "panic", recovered)
		}
	}()

	d.handler(d.ctx, update)
	return true
}

func (d *Dispatcher) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.cleanupIdle()
		}
	}
}

func (d *Dispatcher) cleanupIdle() {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	for chatID, q := range d.queues {
		lastActive := time.Unix(0, q.lastActive.Load())
		if now.Sub(lastActive) < d.idle || len(q.ch) > 0 || q.processing.Load() {
			continue
		}
		delete(d.queues, chatID)
		close(q.ch)
	}
}
