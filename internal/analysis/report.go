package analysis

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

// Timeframe 是单个周期的压缩指标。
type Timeframe struct {
	Trend       string      `json:"trend"`
	EMA20       *float64    `json:"ema20,omitempty"`
	EMA50       *float64    `json:"ema50,omitempty"`
	EMA200      *float64    `json:"ema200,omitempty"`
	RSI14       *float64    `json:"rsi14,omitempty"`
	MACDHist    *float64    `json:"macd_hist,omitempty"`
	ATRPct      *float64    `json:"atr_pct,omitempty"`
	SuperTrend  *SuperTrend `json:"supertrend,omitempty"`
	VolumeRatio *float64    `json:"volume_ratio,omitempty"`
	Support     []float64   `json:"support"`
	Resistance  []float64   `json:"resistance"`
}

// SuperTrend 是单个周期的 SuperTrend 指标快照。
// Direction 为 up 表示价格在 SuperTrend 线之上、趋势偏多；down 相反。
type SuperTrend struct {
	Value         float64 `json:"value"`
	Direction     string  `json:"direction"`
	DistancePct   float64 `json:"distance_pct"`
	BarsSinceFlip int     `json:"bars_since_flip"`
	Period        int     `json:"period"`
	Multiplier    float64 `json:"multiplier"`
}

// Report 是传给 LLM 的完整技术面摘要。
type Report struct {
	Symbol      string               `json:"symbol"`
	Pair        string               `json:"pair"`
	Quote       string               `json:"quote"`
	Market      string               `json:"market"`
	Mode        string               `json:"mode"`
	AsOf        time.Time            `json:"as_of"`
	Price       float64              `json:"price"`
	ChangePct   float64              `json:"change_pct"`
	Timeframes  map[string]Timeframe `json:"timeframes"`
	DataQuality string               `json:"data_quality"`
	Source      string               `json:"source"`
}

// BuildReport 将行情快照转换为多周期技术面报告。
func BuildReport(snapshot *market.Snapshot, mode string) (*Report, error) {
	if snapshot == nil {
		return nil, errors.New("行情快照为空")
	}
	if len(snapshot.Timeframes) == 0 {
		return nil, market.ErrDataUnavailable
	}

	report := &Report{
		Symbol:      snapshot.Symbol.Code,
		Pair:        snapshot.Symbol.Pair,
		Quote:       snapshot.Symbol.Quote,
		Market:      snapshot.Symbol.Market,
		Mode:        mode,
		AsOf:        snapshot.FetchedAt,
		Price:       snapshot.Price,
		ChangePct:   snapshot.ChangePct,
		Timeframes:  make(map[string]Timeframe, len(snapshot.Timeframes)),
		DataQuality: "ok",
		Source:      snapshot.Source,
	}

	for interval, candles := range snapshot.Timeframes {
		timeframe, err := analyzeCandles(candles)
		if err != nil {
			report.DataQuality = "partial"
			continue
		}
		report.Timeframes[interval] = timeframe
	}
	if len(report.Timeframes) == 0 {
		return nil, errors.New("所有周期都缺少足够数据")
	}
	if snapshot.Partial {
		report.DataQuality = "partial"
	}
	return report, nil
}

func analyzeCandles(candles []market.Candle) (Timeframe, error) {
	if len(candles) < 50 {
		return Timeframe{}, errors.New("K 线数量不足")
	}

	closes := make([]float64, len(candles))
	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))
	volumes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
		highs[i] = candle.High
		lows[i] = candle.Low
		volumes[i] = candle.Volume
	}

	price := closes[len(closes)-1]
	result := Timeframe{
		Support:    []float64{},
		Resistance: []float64{},
	}

	ema20, ok20 := emaLast(closes, 20)
	ema50, ok50 := emaLast(closes, 50)
	ema200, ok200 := emaLast(closes, 200)
	if ok20 {
		result.EMA20 = ptr(ema20)
	}
	if ok50 {
		result.EMA50 = ptr(ema50)
	}
	if ok200 {
		result.EMA200 = ptr(ema200)
	}

	if rsi, ok := rsiLast(closes, 14); ok {
		result.RSI14 = ptr(rsi)
	}
	if hist, ok := macdHistLast(closes); ok {
		result.MACDHist = ptr(hist)
	}

	atr, okATR := atrLast(candles, 14)
	if okATR && price > 0 {
		result.ATRPct = ptr(atr / price * 100)
	}
	if superTrend, ok := superTrendLast(candles, superTrendATRPeriod, superTrendMultiplier); ok {
		result.SuperTrend = &superTrend
	}
	result.VolumeRatio = ptr(volumeRatio(volumes, 20))

	result.Support, result.Resistance = supportResistance(candles, atr)
	result.Trend = trend(price, result.EMA20, result.EMA50, result.EMA200)
	return result, nil
}

func trend(price float64, ema20, ema50, ema200 *float64) string {
	if ema20 == nil || ema50 == nil {
		return "sideways"
	}
	up := price > *ema20 && *ema20 > *ema50
	down := price < *ema20 && *ema20 < *ema50
	if ema200 != nil {
		up = up && price > *ema200
		down = down && price < *ema200
	}
	switch {
	case up:
		return "up"
	case down:
		return "down"
	default:
		return "sideways"
	}
}

func emaLast(values []float64, period int) (float64, bool) {
	if len(values) < period || period <= 0 {
		return 0, false
	}
	multiplier := 2.0 / float64(period+1)
	value := 0.0
	for i := 0; i < period; i++ {
		value += values[i]
	}
	value /= float64(period)
	for i := period; i < len(values); i++ {
		value = (values[i]-value)*multiplier + value
	}
	return value, true
}

func emaSeries(values []float64, period int) []float64 {
	result := make([]float64, len(values))
	for i := range result {
		result[i] = math.NaN()
	}
	if len(values) < period || period <= 0 {
		return result
	}
	multiplier := 2.0 / float64(period+1)
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	result[period-1] = sum / float64(period)
	for i := period; i < len(values); i++ {
		result[i] = (values[i]-result[i-1])*multiplier + result[i-1]
	}
	return result
}

func rsiLast(values []float64, period int) (float64, bool) {
	if len(values) <= period || period <= 0 {
		return 0, false
	}
	gainSum, lossSum := 0.0, 0.0
	for i := 1; i <= period; i++ {
		change := values[i] - values[i-1]
		if change >= 0 {
			gainSum += change
		} else {
			lossSum -= change
		}
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	for i := period + 1; i < len(values); i++ {
		change := values[i] - values[i-1]
		gain, loss := 0.0, 0.0
		if change >= 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}

	if avgLoss == 0 {
		if avgGain == 0 {
			return 50, true
		}
		return 100, true
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs), true
}

func macdHistLast(values []float64) (float64, bool) {
	if len(values) < 35 {
		return 0, false
	}
	fast := emaSeries(values, 12)
	slow := emaSeries(values, 26)
	macd := make([]float64, len(values))
	for i := range macd {
		macd[i] = math.NaN()
	}
	for i := 25; i < len(values); i++ {
		if !math.IsNaN(fast[i]) && !math.IsNaN(slow[i]) {
			macd[i] = fast[i] - slow[i]
		}
	}
	// 从第一个有效 MACD 点开始计算信号线。
	start := 25
	signal := emaSeries(macd[start:], 9)
	last := len(signal) - 1
	if last < 0 || math.IsNaN(signal[last]) || math.IsNaN(macd[len(macd)-1]) {
		return 0, false
	}
	return macd[len(macd)-1] - signal[last], true
}

const (
	superTrendATRPeriod  = 10
	superTrendMultiplier = 3.0
)

func atrLast(candles []market.Candle, period int) (float64, bool) {
	if len(candles) <= period || period <= 0 {
		return 0, false
	}
	trs := make([]float64, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		high := candles[i].High
		low := candles[i].Low
		prevClose := candles[i-1].Close
		tr := math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
		trs[i-1] = tr
	}
	if len(trs) < period {
		return 0, false
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)
	for i := period; i < len(trs); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}
	return atr, true
}

// superTrendLast 计算标准 SuperTrend：ATR(period) + multiplier 构建上下轨，
// 返回最后一个周期的线位、方向以及距离最近一次翻转的 K 线数量。
func superTrendLast(candles []market.Candle, period int, multiplier float64) (SuperTrend, bool) {
	if period <= 0 || multiplier <= 0 || len(candles) < period {
		return SuperTrend{}, false
	}
	atr, ok := atrSeries(candles, period)
	if !ok {
		return SuperTrend{}, false
	}

	start := period - 1
	prevUpper := (candles[start].High+candles[start].Low)/2 + multiplier*atr[start]
	prevLower := (candles[start].High+candles[start].Low)/2 - multiplier*atr[start]
	prevTrend := 1
	prevLine := prevLower
	if candles[start].Close < prevLower {
		prevTrend = -1
		prevLine = prevUpper
	}
	barsSinceFlip := 0

	for i := start + 1; i < len(candles); i++ {
		mid := (candles[i].High + candles[i].Low) / 2
		basicUpper := mid + multiplier*atr[i]
		basicLower := mid - multiplier*atr[i]

		upper := basicUpper
		if basicUpper >= prevUpper && candles[i-1].Close <= prevUpper {
			upper = prevUpper
		}
		lower := basicLower
		if basicLower <= prevLower && candles[i-1].Close >= prevLower {
			lower = prevLower
		}

		trend := prevTrend
		line := prevLine
		if prevTrend == 1 {
			if candles[i].Close < lower {
				trend = -1
				line = upper
			} else {
				trend = 1
				line = lower
			}
		} else {
			if candles[i].Close > upper {
				trend = 1
				line = lower
			} else {
				trend = -1
				line = upper
			}
		}

		if trend != prevTrend {
			barsSinceFlip = 0
		} else {
			barsSinceFlip++
		}
		prevUpper, prevLower, prevTrend, prevLine = upper, lower, trend, line
	}

	price := candles[len(candles)-1].Close
	distancePct := 0.0
	if price > 0 {
		distancePct = (price - prevLine) / price * 100
	}
	direction := "down"
	if prevTrend == 1 {
		direction = "up"
	}
	return SuperTrend{
		Value:         prevLine,
		Direction:     direction,
		DistancePct:   distancePct,
		BarsSinceFlip: barsSinceFlip,
		Period:        period,
		Multiplier:    multiplier,
	}, true
}

// atrSeries 返回与 K 线对齐的 Wilder ATR 序列，period-1 之前为 NaN。
func atrSeries(candles []market.Candle, period int) ([]float64, bool) {
	if period <= 0 || len(candles) < period {
		return nil, false
	}
	trs := make([]float64, len(candles))
	for i := range candles {
		if i == 0 {
			trs[i] = candles[i].High - candles[i].Low
			continue
		}
		high := candles[i].High
		low := candles[i].Low
		prevClose := candles[i-1].Close
		trs[i] = math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
	}

	atr := make([]float64, len(candles))
	for i := range atr {
		atr[i] = math.NaN()
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += trs[i]
	}
	atr[period-1] = sum / float64(period)
	for i := period; i < len(candles); i++ {
		atr[i] = (atr[i-1]*float64(period-1) + trs[i]) / float64(period)
	}
	return atr, true
}

func volumeRatio(volumes []float64, period int) float64 {
	if len(volumes) < period || period <= 0 {
		return 0
	}
	sum := 0.0
	for i := len(volumes) - period; i < len(volumes); i++ {
		sum += volumes[i]
	}
	average := sum / float64(period)
	if average == 0 {
		return 0
	}
	return volumes[len(volumes)-1] / average
}

func supportResistance(candles []market.Candle, atr float64) ([]float64, []float64) {
	price := candles[len(candles)-1].Close
	tolerance := atr * 0.5
	if tolerance <= 0 {
		tolerance = price * 0.005
	}

	start := 0
	if len(candles) > 200 {
		start = len(candles) - 200
	}
	var rawSupport, rawResistance []float64
	for i := start + 2; i < len(candles)-2; i++ {
		if isPivotHigh(candles, i) {
			rawResistance = append(rawResistance, candles[i].High)
		}
		if isPivotLow(candles, i) {
			rawSupport = append(rawSupport, candles[i].Low)
		}
	}

	supports := cluster(rawSupport, tolerance)
	resistances := cluster(rawResistance, tolerance)

	supports = below(supports, price)
	resistances = above(resistances, price)
	sortByDistance(supports, price)
	sortByDistance(resistances, price)

	if len(supports) == 0 {
		if low := minLow(candles, 20); low > 0 && low < price {
			supports = []float64{low}
		}
	}
	if len(resistances) == 0 {
		if high := maxHigh(candles, 20); high > price {
			resistances = []float64{high}
		}
	}
	return limit(supports, 3), limit(resistances, 3)
}

func isPivotHigh(candles []market.Candle, i int) bool {
	return candles[i].High > candles[i-1].High &&
		candles[i].High > candles[i-2].High &&
		candles[i].High > candles[i+1].High &&
		candles[i].High > candles[i+2].High
}

func isPivotLow(candles []market.Candle, i int) bool {
	return candles[i].Low < candles[i-1].Low &&
		candles[i].Low < candles[i-2].Low &&
		candles[i].Low < candles[i+1].Low &&
		candles[i].Low < candles[i+2].Low
}

func cluster(levels []float64, tolerance float64) []float64 {
	if len(levels) == 0 {
		return nil
	}
	sorted := append([]float64(nil), levels...)
	sort.Float64s(sorted)

	var result []float64
	group := []float64{sorted[0]}
	for _, level := range sorted[1:] {
		if math.Abs(level-group[len(group)-1]) <= tolerance {
			group = append(group, level)
			continue
		}
		result = append(result, average(group))
		group = []float64{level}
	}
	result = append(result, average(group))
	return result
}

func average(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func below(levels []float64, price float64) []float64 {
	result := make([]float64, 0, len(levels))
	for _, level := range levels {
		if level < price {
			result = append(result, level)
		}
	}
	return result
}

func above(levels []float64, price float64) []float64 {
	result := make([]float64, 0, len(levels))
	for _, level := range levels {
		if level > price {
			result = append(result, level)
		}
	}
	return result
}

func sortByDistance(levels []float64, price float64) {
	sort.Slice(levels, func(i, j int) bool {
		return math.Abs(levels[i]-price) < math.Abs(levels[j]-price)
	})
}

func minLow(candles []market.Candle, count int) float64 {
	start := len(candles) - count
	if start < 0 {
		start = 0
	}
	result := candles[start].Low
	for i := start + 1; i < len(candles); i++ {
		if candles[i].Low < result {
			result = candles[i].Low
		}
	}
	return result
}

func maxHigh(candles []market.Candle, count int) float64 {
	start := len(candles) - count
	if start < 0 {
		start = 0
	}
	result := candles[start].High
	for i := start + 1; i < len(candles); i++ {
		if candles[i].High > result {
			result = candles[i].High
		}
	}
	return result
}

func limit(values []float64, count int) []float64 {
	if len(values) > count {
		return values[:count]
	}
	return values
}

func ptr(value float64) *float64 {
	return &value
}
