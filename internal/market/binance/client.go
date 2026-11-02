package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

const maxResponseBytes = 8 * 1024 * 1024

// Client 是 Binance 现货行情适配器，支持主备 base URL。
type Client struct {
	primary  string
	fallback string
	http     *http.Client

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	expires time.Time
	value   any
}

type PairInfo struct {
	Symbol     string `json:"symbol"`
	BaseAsset  string `json:"baseAsset"`
	QuoteAsset string `json:"quoteAsset"`
	Status     string `json:"status"`
}

type exchangeInfoResponse struct {
	Symbols []PairInfo `json:"symbols"`
}

type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("HTTP %d %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// NewClient 创建 Binance 行情客户端。
func NewClient(primary, fallback string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		primary:  strings.TrimRight(primary, "/"),
		fallback: strings.TrimRight(fallback, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
		cache: make(map[string]cacheEntry),
	}
}

// Resolve 解析用户输入的币种或交易对。
func (c *Client) Resolve(ctx context.Context, query string) (market.Symbol, error) {
	base, _, pair := normalizeQuery(query)
	if pair == "" {
		return market.Symbol{}, market.ErrSymbolNotFound
	}

	candidates := []string{pair}
	usdtPair := base + "USDT"
	if usdtPair != pair {
		candidates = append(candidates, usdtPair)
	}

	for _, candidate := range candidates {
		info, err := c.pairInfo(ctx, candidate)
		if err != nil {
			var statusErr *HTTPStatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusBadRequest {
				continue
			}
			return market.Symbol{}, err
		}
		if info.QuoteAsset != "USDT" {
			continue
		}
		return symbolFromInfo(info)
	}
	return market.Symbol{}, market.ErrSymbolNotFound
}

func symbolFromInfo(item PairInfo) (market.Symbol, error) {
	if item.Status != "TRADING" || item.QuoteAsset != "USDT" {
		return market.Symbol{}, market.ErrSymbolNotFound
	}
	if isLeveragedToken(item.BaseAsset) {
		return market.Symbol{}, market.ErrSymbolNotFound
	}
	return market.Symbol{
		Code:   item.BaseAsset,
		Pair:   item.Symbol,
		Base:   item.BaseAsset,
		Quote:  item.QuoteAsset,
		Market: "crypto",
	}, nil
}

func normalizeQuery(query string) (base, quote, pair string) {
	value := strings.ToUpper(strings.TrimSpace(query))
	value = strings.TrimPrefix(value, "$")
	value = strings.ReplaceAll(value, " ", "")

	if index := strings.IndexAny(value, "/-"); index > 0 {
		base = value[:index]
		quote = value[index+1:]
		if quote == "USD" {
			quote = "USDT"
		}
		return base, quote, base + quote
	}

	for _, suffix := range []string{"USDT", "USDC", "FDUSD"} {
		if strings.HasSuffix(value, suffix) && len(value) > len(suffix) {
			return strings.TrimSuffix(value, suffix), suffix, value
		}
	}
	return value, "USDT", value + "USDT"
}

func isLeveragedToken(base string) bool {
	upper := strings.ToUpper(base)
	if len(upper) <= 3 {
		return false
	}
	for _, suffix := range []string{"UP", "DOWN", "BULL", "BEAR"} {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// Snapshot 拉取价格、24h 涨跌和多周期 K 线。
func (c *Client) Snapshot(ctx context.Context, symbol market.Symbol, timeframes []string) (*market.Snapshot, error) {
	if len(timeframes) == 0 {
		timeframes = market.TimeframesForMode("swing")
	}

	ticker, err := c.ticker24h(ctx, symbol.Pair)
	if err != nil {
		return nil, err
	}

	snapshot := &market.Snapshot{
		Symbol:     symbol,
		Price:      ticker.LastPrice,
		ChangePct:  ticker.ChangePct,
		Timeframes: make(map[string][]market.Candle, len(timeframes)),
		FetchedAt:  time.Now().UTC(),
		Source:     "Binance",
	}

	for _, interval := range timeframes {
		candles, err := c.klines(ctx, symbol.Pair, interval)
		if err != nil {
			return nil, err
		}
		if len(candles) == 0 {
			return nil, fmt.Errorf("%w: %s %s 没有 K 线", market.ErrDataUnavailable, symbol.Pair, interval)
		}
		if len(candles) < 200 {
			snapshot.Partial = true
		}
		snapshot.Timeframes[interval] = candles
	}

	if snapshot.Price <= 0 {
		if candles := snapshot.Timeframes[timeframes[0]]; len(candles) > 0 {
			snapshot.Price = candles[len(candles)-1].Close
		}
	}
	if snapshot.Price <= 0 {
		return nil, market.ErrDataUnavailable
	}
	return snapshot, nil
}

type ticker24h struct {
	LastPrice float64
	ChangePct float64
}

type tickerPayload struct {
	LastPrice          string `json:"lastPrice"`
	PriceChangePercent string `json:"priceChangePercent"`
}

func (c *Client) ticker24h(ctx context.Context, pair string) (ticker24h, error) {
	key := "ticker:" + pair
	if value, ok := c.cacheGet(key); ok {
		if result, ok := value.(ticker24h); ok {
			return result, nil
		}
	}

	var payload tickerPayload
	if err := c.getJSON(ctx, "/api/v3/ticker/24hr?symbol="+pair, &payload); err != nil {
		return ticker24h{}, err
	}
	price, err := strconv.ParseFloat(payload.LastPrice, 64)
	if err != nil || price <= 0 {
		return ticker24h{}, fmt.Errorf("%w: 无法解析价格", market.ErrDataUnavailable)
	}
	change, _ := strconv.ParseFloat(payload.PriceChangePercent, 64)
	result := ticker24h{LastPrice: price, ChangePct: change}
	c.cacheSet(key, result, 20*time.Second)
	return result, nil
}

func (c *Client) klines(ctx context.Context, pair, interval string) ([]market.Candle, error) {
	key := "klines:" + pair + ":" + interval
	if value, ok := c.cacheGet(key); ok {
		if result, ok := value.([]market.Candle); ok {
			return result, nil
		}
	}

	var rows [][]json.RawMessage
	path := fmt.Sprintf("/api/v3/klines?symbol=%s&interval=%s&limit=500", pair, interval)
	if err := c.getJSON(ctx, path, &rows); err != nil {
		return nil, err
	}

	candles := make([]market.Candle, 0, len(rows))
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		openTimeMillis, err := parseIntRaw(row[0])
		if err != nil {
			continue
		}
		open, err1 := parseFloatRaw(row[1])
		high, err2 := parseFloatRaw(row[2])
		low, err3 := parseFloatRaw(row[3])
		closePrice, err4 := parseFloatRaw(row[4])
		volume, err5 := parseFloatRaw(row[5])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
			continue
		}
		candles = append(candles, market.Candle{
			OpenTime: time.UnixMilli(openTimeMillis).UTC(),
			Open:     open,
			High:     high,
			Low:      low,
			Close:    closePrice,
			Volume:   volume,
		})
	}
	if len(candles) == 0 {
		return nil, fmt.Errorf("%w: %s %s K 线为空", market.ErrDataUnavailable, pair, interval)
	}
	c.cacheSet(key, candles, time.Minute)
	return candles, nil
}

func (c *Client) pairInfo(ctx context.Context, pair string) (PairInfo, error) {
	key := "exchangeInfo:" + pair
	if value, ok := c.cacheGet(key); ok {
		if result, ok := value.(PairInfo); ok {
			return result, nil
		}
	}

	var payload exchangeInfoResponse
	if err := c.getJSON(ctx, "/api/v3/exchangeInfo?symbol="+pair, &payload); err != nil {
		return PairInfo{}, err
	}
	for _, item := range payload.Symbols {
		if item.Symbol == pair {
			c.cacheSet(key, item, 24*time.Hour)
			return item, nil
		}
	}
	return PairInfo{}, &HTTPStatusError{StatusCode: http.StatusBadRequest, Body: "symbol not found"}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	bases := []string{c.primary}
	if c.fallback != "" && c.fallback != c.primary {
		bases = append(bases, c.fallback)
	}

	var lastErr error
	for _, base := range bases {
		endpoint := base + path
		for attempt := 0; attempt < 3; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", "guanlanai/phase3")

			resp, err := c.http.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("%w: %v", market.ErrDataUnavailable, err)
				break
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("%w: %v", market.ErrDataUnavailable, readErr)
				break
			}
			if int64(len(body)) > maxResponseBytes {
				lastErr = fmt.Errorf("%w: 响应过大", market.ErrDataUnavailable)
				break
			}

			switch {
			case resp.StatusCode == http.StatusOK:
				if err := json.Unmarshal(body, out); err != nil {
					return fmt.Errorf("%w: 行情 JSON 解析失败", market.ErrDataUnavailable)
				}
				return nil
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				lastErr = &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
				if attempt < 2 {
					if err := sleepContext(ctx, time.Duration(attempt+1)*300*time.Millisecond); err != nil {
						return err
					}
					continue
				}
			case resp.StatusCode == 451 || resp.StatusCode == 403:
				lastErr = &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
			default:
				return &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
			}
			break
		}
	}
	if lastErr == nil {
		lastErr = market.ErrDataUnavailable
	}
	return lastErr
}

func (c *Client) cacheGet(key string) (any, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(c.cache, key)
		}
		return nil, false
	}
	return entry.value, true
}

func (c *Client) cacheSet(key string, value any, ttl time.Duration) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	c.cache[key] = cacheEntry{expires: time.Now().Add(ttl), value: value}
}

func parseFloatRaw(raw json.RawMessage) (float64, error) {
	value := strings.Trim(string(raw), `"`)
	return strconv.ParseFloat(value, 64)
}

func parseIntRaw(raw json.RawMessage) (int64, error) {
	value := strings.Trim(string(raw), `"`)
	return strconv.ParseInt(value, 10, 64)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ market.Resolver = (*Client)(nil)
var _ market.Provider = (*Client)(nil)
