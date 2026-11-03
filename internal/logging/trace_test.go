package logging

import (
	"context"
	"testing"
)

func TestEnsureTraceIsStable(t *testing.T) {
	ctx, id := EnsureTrace(context.Background())
	if id == "" {
		t.Fatal("trace ID 为空")
	}
	again, same := EnsureTrace(ctx)
	if same != id {
		t.Fatalf("同一 context 应复用 trace: %q -> %q", id, same)
	}
	if TraceID(again) != id {
		t.Fatalf("TraceID() = %q, want %q", TraceID(again), id)
	}
}

func TestEnsureTraceCreatesNewID(t *testing.T) {
	_, first := EnsureTrace(context.Background())
	_, second := EnsureTrace(context.Background())
	if first == second {
		t.Fatalf("不同请求应生成不同 trace: %q", first)
	}
}
