package ratelimit

import (
	"sync"
	"time"
)

// Limiter 是基于固定窗口的内存限流器，适合单实例部署。
type Limiter struct {
	mu sync.Mutex

	analysisPerMinute int
	analysisPerHour   int
	chatPerMinute     int

	buckets map[int64]*bucket
}

type bucket struct {
	minuteStart     time.Time
	hourStart       time.Time
	minuteCount     int
	hourCount       int
	chatMinuteCount int
}

// New 创建限流器。
func New(analysisPerMinute, analysisPerHour, chatPerMinute int) *Limiter {
	if analysisPerMinute <= 0 {
		analysisPerMinute = 6
	}
	if analysisPerHour <= 0 {
		analysisPerHour = 30
	}
	if chatPerMinute <= 0 {
		chatPerMinute = 20
	}
	return &Limiter{
		analysisPerMinute: analysisPerMinute,
		analysisPerHour:   analysisPerHour,
		chatPerMinute:     chatPerMinute,
		buckets:           make(map[int64]*bucket),
	}
}

// AllowAnalysis 检查分析请求是否允许，并返回建议的重试等待时间。
func (l *Limiter) AllowAnalysis(userID int64) (bool, time.Duration) {
	return l.allowAnalysisAt(userID, time.Now())
}

// AllowChat 检查追问请求是否允许。
func (l *Limiter) AllowChat(userID int64) (bool, time.Duration) {
	return l.allowChatAt(userID, time.Now())
}

func (l *Limiter) allowAnalysisAt(userID int64, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucket(userID, now)

	if b.minuteCount >= l.analysisPerMinute {
		return false, b.minuteStart.Add(time.Minute).Sub(now)
	}
	if b.hourCount >= l.analysisPerHour {
		return false, b.hourStart.Add(time.Hour).Sub(now)
	}
	b.minuteCount++
	b.hourCount++
	return true, 0
}

func (l *Limiter) allowChatAt(userID int64, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucket(userID, now)

	if b.chatMinuteCount >= l.chatPerMinute {
		return false, b.minuteStart.Add(time.Minute).Sub(now)
	}
	b.chatMinuteCount++
	return true, 0
}

func (l *Limiter) bucket(userID int64, now time.Time) *bucket {
	b := l.buckets[userID]
	if b == nil {
		b = &bucket{minuteStart: now, hourStart: now}
		l.buckets[userID] = b
	}
	if now.Sub(b.minuteStart) >= time.Minute {
		b.minuteStart = now
		b.minuteCount = 0
		b.chatMinuteCount = 0
	}
	if now.Sub(b.hourStart) >= time.Hour {
		b.hourStart = now
		b.hourCount = 0
	}
	return b
}

// CleanupIdle 删除长时间不活跃的用户，避免 map 无限增长。
func (l *Limiter) CleanupIdle(now time.Time, idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for userID, b := range l.buckets {
		if now.Sub(b.minuteStart) > idle && now.Sub(b.hourStart) > idle {
			delete(l.buckets, userID)
		}
	}
}
