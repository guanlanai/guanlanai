package market

import (
	"sync"
	"time"
)

// SeriesTTL 返回某个周期允许复用行情快照的时间。
func SeriesTTL(timeframe Timeframe) time.Duration {
	switch timeframe {
	case TF1m, TF5m, TF15m, TF30m:
		return 2 * time.Minute
	case TF1h, TF2h, TF4h, TF6h, TF12h:
		return 10 * time.Minute
	case TF1D, TF1W:
		return 30 * time.Minute
	case TF1M:
		return 2 * time.Hour
	default:
		return 10 * time.Minute
	}
}

// SnapshotCache 在进程内保存每个会话最近一次行情快照，供追问、切换指标等场景复用。
// 只保留最近的一次快照，按周期 TTL 判断是否仍然可用。
type SnapshotCache struct {
	mu      sync.Mutex
	entries map[int64]snapshotCacheEntry
	max     int
	now     func() time.Time
}

type snapshotCacheEntry struct {
	snapshot *Snapshot
	storedAt time.Time
}

// NewSnapshotCache 创建会话快照缓存；max 为最多保留的会话数。
func NewSnapshotCache(max int) *SnapshotCache {
	if max <= 0 {
		max = 200
	}
	return &SnapshotCache{
		entries: make(map[int64]snapshotCacheEntry),
		max:     max,
		now:     time.Now,
	}
}

// Get 返回会话中与 symbol/timeframe 匹配且未过期的快照。
func (c *SnapshotCache) Get(sessionID int64, symbol Symbol, timeframe Timeframe) (*Snapshot, bool) {
	return c.GetMany(sessionID, symbol, []Timeframe{timeframe})
}

// GetMany 返回包含全部所需周期且都未过期的会话快照。
func (c *SnapshotCache) GetMany(sessionID int64, symbol Symbol, timeframes []Timeframe) (*Snapshot, bool) {
	if c == nil || sessionID == 0 || len(timeframes) == 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[sessionID]
	if !ok || entry.snapshot == nil {
		return nil, false
	}
	if entry.snapshot.Symbol.Pair != symbol.Pair {
		return nil, false
	}
	fetchedAt := entry.snapshot.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = entry.storedAt
	}
	age := c.now().Sub(fetchedAt)
	for _, timeframe := range timeframes {
		if _, ok := entry.snapshot.Series[timeframe]; !ok {
			return nil, false
		}
		if age > SeriesTTL(timeframe) {
			delete(c.entries, sessionID)
			return nil, false
		}
	}
	return entry.snapshot, true
}

// Put 保存会话最近一次快照，并按会话数上限淘汰最旧记录。
func (c *SnapshotCache) Put(sessionID int64, snapshot *Snapshot) {
	if c == nil || sessionID == 0 || snapshot == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if _, exists := c.entries[sessionID]; !exists && len(c.entries) >= c.max {
		c.evictOldestLocked()
	}
	c.entries[sessionID] = snapshotCacheEntry{snapshot: snapshot, storedAt: now}
}

func (c *SnapshotCache) evictOldestLocked() {
	var oldestID int64
	var oldest time.Time
	for sessionID, entry := range c.entries {
		if oldestID == 0 || entry.storedAt.Before(oldest) {
			oldestID = sessionID
			oldest = entry.storedAt
		}
	}
	if oldestID != 0 {
		delete(c.entries, oldestID)
	}
}
