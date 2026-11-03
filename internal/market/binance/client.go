package binance

import (
	"container/list"
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
	"github.com/guanlanai/guanlanai/internal/resilience"
)

const (
	maxResponseBytes = 8 * 1024 * 1024
	maxCacheEntries  = 1000
)

// Client 是 Binance 现货行情适配器，支持主备 base URL。
type Client struct {
	primary  string
	fallback string
	http     *http.Client
	breaker  *resilience.Breaker

	cacheMu sync.Mutex
	cache   map[string]*list.Element
	order   *list.List
}

type cacheEntry struct {
	key     string
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
		breaker: resilience.New(5, 30*time.Second),
		cache:   make(map[string]*list.Element),
		order:   list.New(),
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

// intervalByTimeframe 只在本适配器内把标准周期映射成 Binance interval。
var intervalByTimeframe = map[market.Timeframe]string{
	market.TF1m:  "1m",
	market.TF5m:  "5m",
	market.TF15m: "15m",
	market.TF30m: "30m",
	market.TF1h:  "1h",
	market.TF2h:  "2h",
	market.TF4h:  "4h",
	market.TF6h:  "6h",
	market.TF12h: "12h",
	market.TF1D:  "1d",
	market.TF1W:  "1w",
	market.TF1M:  "1M",
}

func intervalFor(timeframe market.Timeframe) (string, error) {
	if interval, ok := intervalByTimeframe[timeframe]; ok {
		return interval, nil
	}
	return "", fmt.Errorf("%w: %q", market.ErrInvalidTimeframe, timeframe)
}

// Snapshot 拉取价格、24h 涨跌和所需周期的 K 线。
func (c *Client) Snapshot(ctx context.Context, symbol market.Symbol, timeframes []market.Timeframe) (*market.Snapshot, error) {
	if len(timeframes) == 0 {
		timeframes = []market.Timeframe{market.DefaultTimeframe}
	}

	ticker, err := c.ticker24h(ctx, symbol.Pair)
	if err != nil {
		return nil, err
	}

	snapshot := &market.Snapshot{
		Symbol:    symbol,
		Price:     ticker.LastPrice,
		ChangePct: ticker.ChangePct,
		Series:    make(map[market.Timeframe]market.Series, len(timeframes)),
		FetchedAt: time.Now().UTC(),
		Source:    "Binance",
	}

	type fetchResult struct {
		timeframe market.Timeframe
		candles   []market.Candle
		err       error
	}
	results := make(chan fetchResult, len(timeframes))
	for _, timeframe := range timeframes {
		go func(timeframe market.Timeframe) {
			interval, err := intervalFor(timeframe)
			if err != nil {
				results <- fetchResult{timeframe: timeframe, err: err}
				return
			}
			candles, err := c.klines(ctx, symbol.Pair, timeframe, interval)
			if err == nil && len(candles) == 0 {
				err = fmt.Errorf("%w: %s %s 没有 K 线", market.ErrDataUnavailable, symbol.Pair, interval)
			}
			results <- fetchResult{timeframe: timeframe, candles: candles, err: err}
		}(timeframe)
	}

	var firstErr error
	for range timeframes {
		result := <-results
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		snapshot.Series[result.timeframe] = market.Series{
			Timeframe: result.timeframe,
			Candles:   result.candles,
			Partial:   len(result.candles) < 200,
			AsOf:      result.candles[len(result.candles)-1].OpenTime,
		}
	}

	if len(snapshot.Series) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, market.ErrDataUnavailable
	}
	if snapshot.Price <= 0 {
		if series, ok := snapshot.Series[timeframes[0]]; ok && len(series.Candles) > 0 {
			snapshot.Price = series.Candles[len(series.Candles)-1].Close
		}
	}
	if snapshot.Price <= 0 {
		for _, series := range snapshot.Series {
			if len(series.Candles) > 0 {
				snapshot.Price = series.Candles[len(series.Candles)-1].Close
				break
			}
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

func (c *Client) klines(ctx context.Context, pair string, timeframe market.Timeframe, interval string) ([]market.Candle, error) {
	key := "klines:" + pair + ":" + timeframe.String()
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
		var closeTime time.Time
		if len(row) > 6 {
			if closeTimeMillis, err := parseIntRaw(row[6]); err == nil {
				closeTime = time.UnixMilli(closeTimeMillis).UTC()
			}
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
			OpenTime:  time.UnixMilli(openTimeMillis).UTC(),
			CloseTime: closeTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
		})
	}
	if len(candles) == 0 {
		return nil, fmt.Errorf("%w: %s %s K 线为空", market.ErrDataUnavailable, pair, interval)
	}
	c.cacheSet(key, candles, market.SeriesTTL(timeframe))
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
	if err := c.breaker.Allow(); err != nil {
		return fmt.Errorf("%w: %w", market.ErrDataUnavailable, err)
	}
	err := c.getJSONOnce(ctx, path, out)
	c.breaker.Record(!marketFailure(err))
	return err
}

func marketFailure(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusTooManyRequests ||
			statusErr.StatusCode == 451 ||
			statusErr.StatusCode >= 500
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func (c *Client) getJSONOnce(ctx context.Context, path string, out any) error {
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
	element, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	entry := element.Value.(*cacheEntry)
	if time.Now().After(entry.expires) {
		c.order.Remove(element)
		delete(c.cache, key)
		return nil, false
	}
	c.order.MoveToFront(element)
	return entry.value, true
}

func (c *Client) cacheSet(key string, value any, ttl time.Duration) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	if element, ok := c.cache[key]; ok {
		entry := element.Value.(*cacheEntry)
		entry.expires = time.Now().Add(ttl)
		entry.value = value
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(&cacheEntry{key: key, expires: time.Now().Add(ttl), value: value})
	c.cache[key] = element
	for c.order.Len() > maxCacheEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(*cacheEntry)
		c.order.Remove(oldest)
		delete(c.cache, entry.key)
	}
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
