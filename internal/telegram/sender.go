package telegram

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/time/rate"

	"github.com/guanlanai/guanlanai/internal/render"
)

// RateLimitedSender 为 Telegram 发送增加每 chat 限速、全局限速和 429 重试。
type RateLimitedSender struct {
	next     render.Sender
	perChat  time.Duration
	global   *rate.Limiter
	retries  int
	baseWait time.Duration

	mu       sync.Mutex
	lastSent map[int64]time.Time
}

// NewRateLimitedSender 创建发送包装器。
func NewRateLimitedSender(next render.Sender, perChat time.Duration, globalPerSecond int, retries int) *RateLimitedSender {
	if globalPerSecond <= 0 {
		globalPerSecond = 25
	}
	if retries < 0 {
		retries = 0
	}
	return &RateLimitedSender{
		next:     next,
		perChat:  perChat,
		global:   rate.NewLimiter(rate.Limit(globalPerSecond), globalPerSecond),
		retries:  retries,
		baseWait: time.Second,
		lastSent: make(map[int64]time.Time),
	}
}

func (s *RateLimitedSender) SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	chatID, _ := params.ChatID.(int64)

	var lastErr error
	for attempt := 0; attempt <= s.retries; attempt++ {
		if err := s.waitChat(ctx, chatID); err != nil {
			return nil, err
		}
		if err := s.global.Wait(ctx); err != nil {
			return nil, err
		}

		response, err := s.next.SendMessage(ctx, params)
		if err == nil {
			return response, nil
		}
		lastErr = err

		var tooMany *bot.TooManyRequestsError
		if errors.As(err, &tooMany) {
			wait := time.Duration(tooMany.RetryAfter) * time.Second
			if wait <= 0 {
				wait = s.baseWait
			}
			if !sleepWithContext(ctx, wait) {
				return nil, ctx.Err()
			}
			continue
		}
		if isTransientSendError(err) && attempt < s.retries {
			if !sleepWithContext(ctx, s.baseWait*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("Telegram 发送重试耗尽: %w", lastErr)
}

func (s *RateLimitedSender) waitChat(ctx context.Context, chatID int64) error {
	if s.perChat <= 0 || chatID == 0 {
		return nil
	}
	for {
		s.mu.Lock()
		now := time.Now()
		if len(s.lastSent) > 10000 {
			for id, sentAt := range s.lastSent {
				if now.Sub(sentAt) > 10*time.Minute {
					delete(s.lastSent, id)
				}
			}
		}
		last := s.lastSent[chatID]
		wait := last.Add(s.perChat).Sub(now)
		if wait <= 0 {
			s.lastSent[chatID] = now
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		if !sleepWithContext(ctx, wait) {
			return ctx.Err()
		}
	}
}

func isTransientSendError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
