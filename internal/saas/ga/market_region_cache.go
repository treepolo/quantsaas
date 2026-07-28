package ga

import (
	"container/list"
	"sync"

	"quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
)

// MarketRegionFeatureCache is scoped to one evolution epoch or one backtest.
// It caches only immutable, exactly computed causal feature series. It never
// persists data and is deliberately bounded so large integer-window searches
// cannot consume unbounded process memory.
type MarketRegionFeatureCache struct {
	mu       sync.Mutex
	maxBytes int64
	used     int64
	entries  map[marketRegionCacheKey]*marketRegionCacheEntry
	lru      *list.List
}

type marketRegionCacheKey struct {
	first  *quant.Bar
	length int
	window int
}

type marketRegionCacheEntry struct {
	key     marketRegionCacheKey
	series  marketRegionFeatureSeries
	bytes   int64
	err     error
	ready   chan struct{}
	element *list.Element
}

type marketRegionFeatureSeries struct {
	activity []dynamicparam.FeaturePoint
	geometry []dynamicparam.GeometryPoint
}

// NewMarketRegionFeatureCache creates an exact LRU cache with a 256 MiB ceiling.
// A value larger than the ceiling is still computed correctly for its caller but
// is not retained. The cap applies per task, not globally across users.
func NewMarketRegionFeatureCache() *MarketRegionFeatureCache {
	return &MarketRegionFeatureCache{
		maxBytes: 256 << 20,
		entries:  make(map[marketRegionCacheKey]*marketRegionCacheEntry),
		lru:      list.New(),
	}
}

func (c *MarketRegionFeatureCache) series(bars []quant.Bar, window int) (marketRegionFeatureSeries, error) {
	if c == nil || len(bars) == 0 {
		return buildMarketRegionFeatureSeries(bars, window)
	}
	key := marketRegionCacheKey{first: &bars[0], length: len(bars), window: window}
	c.mu.Lock()
	if entry := c.entries[key]; entry != nil {
		if entry.element != nil {
			c.lru.MoveToFront(entry.element)
		}
		ready := entry.ready
		c.mu.Unlock()
		<-ready
		return entry.series, entry.err
	}
	entry := &marketRegionCacheEntry{key: key, ready: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()

	entry.series, entry.err = buildMarketRegionFeatureSeries(bars, window)
	entry.bytes = marketRegionFeatureSeriesBytes(entry.series)

	c.mu.Lock()
	if entry.err != nil || entry.bytes > c.maxBytes {
		delete(c.entries, key)
	} else {
		entry.element = c.lru.PushFront(entry)
		c.used += entry.bytes
		for c.used > c.maxBytes && c.lru.Len() > 1 {
			oldest := c.lru.Back()
			old := oldest.Value.(*marketRegionCacheEntry)
			c.lru.Remove(oldest)
			delete(c.entries, old.key)
			c.used -= old.bytes
		}
	}
	close(entry.ready)
	c.mu.Unlock()
	return entry.series, entry.err
}

func buildMarketRegionFeatureSeries(bars []quant.Bar, window int) (marketRegionFeatureSeries, error) {
	activity, err := dynamicparam.BuildFeaturePointsWithoutRawSequence(bars, window)
	if err != nil {
		return marketRegionFeatureSeries{}, err
	}
	geometry, err := dynamicparam.BuildGeometryFeatures(bars, window)
	if err != nil {
		return marketRegionFeatureSeries{}, err
	}
	return marketRegionFeatureSeries{activity: activity, geometry: geometry}, nil
}

func marketRegionFeatureSeriesBytes(series marketRegionFeatureSeries) int64 {
	return int64(len(series.activity))*int64(unsafeSizeFeaturePoint) + int64(len(series.geometry))*int64(unsafeSizeGeometryPoint)
}

// These are conservative accounting values, intentionally larger than the
// payload fields. They avoid unsafe.Sizeof and keep cache accounting stable.
const (
	unsafeSizeFeaturePoint  = 192
	unsafeSizeGeometryPoint = 64
)
