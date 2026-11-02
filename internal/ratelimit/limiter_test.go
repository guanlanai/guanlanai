package ratelimit

import (
	"testing"
	"time"
)

func TestAllowAnalysis(t *testing.T) {
	limiter := New(2, 3, 2)
	now := time.Now()

	if ok, _ := limiter.allowAnalysisAt(1, now); !ok {
		t.Fatal("第 1 次分析应当允许")
	}
	if ok, _ := limiter.allowAnalysisAt(1, now); !ok {
		t.Fatal("第 2 次分析应当允许")
	}
	ok, retry := limiter.allowAnalysisAt(1, now)
	if ok {
		t.Fatal("第 3 次分析应当被限流")
	}
	if retry <= 0 {
		t.Fatalf("retry = %v", retry)
	}

	if ok, _ := limiter.allowAnalysisAt(1, now.Add(time.Minute)); !ok {
		t.Fatal("下一分钟应当恢复")
	}
}

func TestAllowChat(t *testing.T) {
	limiter := New(10, 10, 1)
	now := time.Now()

	if ok, _ := limiter.allowChatAt(1, now); !ok {
		t.Fatal("第 1 次追问应当允许")
	}
	if ok, _ := limiter.allowChatAt(1, now); ok {
		t.Fatal("第 2 次追问应当被限流")
	}
	if ok, _ := limiter.allowChatAt(2, now); !ok {
		t.Fatal("不同用户互不影响")
	}
}

func TestCleanupIdle(t *testing.T) {
	limiter := New(1, 1, 1)
	now := time.Now()
	limiter.allowAnalysisAt(1, now)
	limiter.CleanupIdle(now.Add(2*time.Hour), time.Hour)
	if len(limiter.buckets) != 0 {
		t.Fatalf("buckets = %d, want 0", len(limiter.buckets))
	}
}
