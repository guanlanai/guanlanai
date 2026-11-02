package usage

import (
	"context"
)

// Event 是一次 LLM 调用的用量记录。
type Event struct {
	UserID           int64
	RequestID        string
	Kind             string // analysis / chat / summarize
	Model            string
	PromptTokens     int
	CompletionTokens int
	LatencyMS        int
	Status           string // ok / error
}

// Summary 是用户当天的用量汇总。
type Summary struct {
	Requests         int64
	PromptTokens     int64
	CompletionTokens int64
}

// Store 是用量持久化接口。
type Store interface {
	Record(ctx context.Context, event Event) error
	Today(ctx context.Context, userID int64) (Summary, error)
}
