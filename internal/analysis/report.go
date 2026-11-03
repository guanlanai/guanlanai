package analysis

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/guanlanai/guanlanai/internal/market"
)

// Indicators 是单个 screen 的压缩指标。
type Indicators struct {
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

// Screen 是分析计划中的一个周期视图。
// Phase 1 只有 role=primary 的一个 screen；多周期策略只需增加 screen。
type Screen struct {
	Role            string     `json:"role"`
	Timeframe       string     `json:"timeframe"`
	AsOf            time.Time  `json:"as_of"`
	IncludesForming bool       `json:"includes_forming"`
	DataQuality     string     `json:"data_quality"`
	Indicators      Indicators `json:"indicators"`
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

// Alignment 是三重滤网的确定性跨周期结论，由代码计算，LLM 只负责解释。
type Alignment struct {
	Tide     string `json:"tide"`
	Wave     string `json:"wave"`
	Entry    string `json:"entry"`
	Conflict bool   `json:"conflict"`
}

// Report 是传给 LLM 的完整技术面摘要。
// Strategy=single 时 Screens 长度为 1、Role=primary；三重滤网时包含三个 screen。
type Report struct {
	Symbol      string     `json:"symbol"`
	Pair        string     `json:"pair"`
	Quote       string     `json:"quote"`
	Market      string     `json:"market"`
	Mode        string     `json:"mode"`
	Strategy    string     `json:"strategy"`
	AsOf        time.Time  `json:"as_of"`
	Price       float64    `json:"price"`
	ChangePct   float64    `json:"change_pct"`
	Screens     []Screen   `json:"screens"`
	Alignment   *Alignment `json:"alignment,omitempty"`
	DataQuality string     `json:"data_quality"`
	Source      string     `json:"source"`
}

// BuildReport 构建单周期报告；timeframe 为空时使用系统默认周期。
// indicators 为空表示使用默认全部指标；ATR 和支撑阻力始终保留。
func BuildReport(snapshot *market.Snapshot, mode string, timeframe market.Timeframe, indicators []Indicator) (*Report, error) {
	if timeframe == "" {
		timeframe = market.DefaultTimeframe
	}
	return BuildStrategyReport(snapshot, mode, StrategySingle,
		[]ScreenSpec{{Role: "primary", Timeframe: timeframe}}, indicators)
}

// BuildStrategyReport 按策略声明的 screen 列表构建报告。
// 某个 screen 缺失或数据不足时降级为 partial，而不是让整次分析失败。
func BuildStrategyReport(snapshot *market.Snapshot, mode string, strategy Strategy, specs []ScreenSpec, indicators []Indicator) (*Report, error) {
	if snapshot == nil {
		return nil, errors.New("行情快照为空")
	}
	if len(snapshot.Series) == 0 {
		return nil, market.ErrDataUnavailable
	}
	if len(specs) == 0 {
		return nil, errors.New("分析计划没有 screen")
	}

	report := &Report{
		Symbol:      snapshot.Symbol.Code,
		Pair:        snapshot.Symbol.Pair,
		Quote:       snapshot.Symbol.Quote,
		Market:      snapshot.Symbol.Market,
		Mode:        mode,
		Strategy:    string(strategy),
		AsOf:        snapshot.FetchedAt,
		Price:       snapshot.Price,
		ChangePct:   snapshot.ChangePct,
		Screens:     make([]Screen, 0, len(specs)),
		DataQuality: "ok",
		Source:      snapshot.Source,
	}

	present := 0
	alignmentScreens := make([]Screen, 0, len(specs))
	for _, spec := range specs {
		screen := Screen{
			Role:        spec.Role,
			Timeframe:   spec.Timeframe.String(),
			AsOf:        snapshot.FetchedAt,
			DataQuality: "partial",
		}
		series, ok := snapshot.Series[spec.Timeframe]
		if !ok || len(series.Candles) == 0 {
			report.DataQuality = "partial"
			report.Screens = append(report.Screens, screen)
			continue
		}

		present++
		screen.DataQuality = "ok"
		if !series.AsOf.IsZero() {
			screen.AsOf = series.AsOf
		}
		candles, includesForming := selectCandles(series.Candles, spec.Timeframe, time.Now().UTC())
		screen.IncludesForming = includesForming
		if len(candles) > 0 {
			screen.AsOf = candles[len(candles)-1].OpenTime
		}
		analyzed, err := analyzeCandles(candles)
		if err != nil {
			// 数据不足时降级为 partial screen，而不是让整次分析失败。
			screen.DataQuality = "partial"
			report.DataQuality = "partial"
		} else {
			// alignment 使用完整指标；报告只保留用户选择的指标。
			alignmentScreens = append(alignmentScreens, Screen{
				Role:       spec.Role,
				Timeframe:  spec.Timeframe.String(),
				Indicators: analyzed,
			})
			screen.Indicators = filterIndicators(analyzed, indicators)
			if strategy == StrategyTripleScreen {
				screen.Indicators = compactIndicators(screen.Indicators)
			}
		}
		if series.Partial {
			screen.DataQuality = "partial"
			report.DataQuality = "partial"
		}
		report.Screens = append(report.Screens, screen)
	}

	if present == 0 {
		return nil, fmt.Errorf("%w: 快照缺少所有所需周期", market.ErrDataUnavailable)
	}
	if strategy == StrategyTripleScreen {
		report.Alignment = computeAlignment(alignmentScreens)
	}
	if len(report.Screens) == 1 {
		report.AsOf = report.Screens[0].AsOf
	}
	if report.AsOf.IsZero() {
		report.AsOf = snapshot.FetchedAt
	}
	return report, nil
}

// compactIndicators 压缩多周期报告，控制提示词长度。
func compactIndicators(value Indicators) Indicators {
	value.Support = limit(value.Support, 2)
	value.Resistance = limit(value.Resistance, 2)
	return value
}

// selectCandles 区分已收盘和正在形成的 K 线。
// 日线及以上默认丢弃正在形成的当前 K 线，避免趋势指标漂移；低周期保留并标记。
func selectCandles(candles []market.Candle, timeframe market.Timeframe, now time.Time) ([]market.Candle, bool) {
	if len(candles) == 0 {
		return candles, false
	}
	last := candles[len(candles)-1]
	forming := !last.CloseTime.IsZero() && last.CloseTime.After(now)
	if !forming {
		return candles, false
	}
	switch timeframe {
	case market.TF1D, market.TF1W, market.TF1M:
		return candles[:len(candles)-1], false
	default:
		return candles, true
	}
}

func analyzeCandles(candles []market.Candle) (Indicators, error) {
	if len(candles) < 50 {
		return Indicators{}, errors.New("K 线数量不足")
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
	result := Indicators{
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

// filterIndicators 只保留用户选择的指标组；trend、ATR 和支撑阻力始终保留。
func filterIndicators(value Indicators, selection []Indicator) Indicators {
	if len(selection) == 0 {
		return value
	}
	if !HasIndicator(selection, IndicatorEMA) {
		value.EMA20 = nil
		value.EMA50 = nil
		value.EMA200 = nil
	}
	if !HasIndicator(selection, IndicatorRSI) {
		value.RSI14 = nil
	}
	if !HasIndicator(selection, IndicatorMACD) {
		value.MACDHist = nil
	}
	if !HasIndicator(selection, IndicatorSuperTrend) {
		value.SuperTrend = nil
	}
	if !HasIndicator(selection, IndicatorVolume) {
		value.VolumeRatio = nil
	}
	return value
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
