package ga

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"quantsaas/internal/quant"
)

const layeredGridCheckpointVersion = 2

// coreSearchStep is the formal lattice requested by the parameter laboratory.
// Values are created on this lattice; they are never sampled continuously and
// rounded afterwards. Integer-valued months use one month, which is itself an
// exact multiple of 0.05.
const coreSearchStep = 0.05

type layeredAxis struct {
	Key       string
	Minimum   int64
	Maximum   int64
	Centre    int64
	Lower     int64
	Upper     int64
	Unbounded bool
}

type layeredSlice struct {
	Axis   int
	Value  int64
	Cursor uint64
	Total  uint64
}

type layeredGridCheckpoint struct {
	Version        int               `json:"version"`
	Options        GeneOptions       `json:"options"`
	Region         *MarketRegionGene `json:"region,omitempty"`
	Centre         quant.Chromosome  `json:"centre"`
	Axes           []layeredAxis     `json:"axes"`
	Slice          layeredSlice      `json:"slice"`
	FirstPending   bool              `json:"first_pending"`
	ExpandAxis     int               `json:"expand_axis"`
	ExpandUpper    bool              `json:"expand_upper"`
	GlobalCursor   uint64            `json:"global_cursor"`
	Issued         uint64            `json:"issued"`
	DuplicateSkips uint64            `json:"duplicate_skips"`
	LocalPercent   int               `json:"local_percent"`
	PendingCentre  *quant.Chromosome `json:"pending_centre,omitempty"`
	PendingRegion  *MarketRegionGene `json:"pending_region,omitempty"`
}

// layeredGridScheduler enumerates one explicit box. When that box is complete,
// it expands exactly one side of exactly one axis and enumerates only the newly
// added slice. A separate deterministic global frontier provides the configured
// exploration share. No random samples, projection, truncation, or retry cap
// are used.
type layeredGridScheduler struct {
	options        GeneOptions
	region         *MarketRegionGene
	centre         quant.Chromosome
	axes           []layeredAxis
	axisIndex      map[string]int
	slice          layeredSlice
	firstPending   bool
	expandAxis     int
	expandUpper    bool
	globalCursor   uint64
	issued         uint64
	duplicateSkips uint64
	localPercent   int
	pendingCentre  *quant.Chromosome
	pendingRegion  *MarketRegionGene
}

func newLayeredGridScheduler(seed Gene, options GeneOptions, localPercent int) *layeredGridScheduler {
	options = NormalizeGeneOptions(options)
	var centre quant.Chromosome
	var region *MarketRegionGene
	if value, ok := isMarketRegionGene(seed); ok {
		copy := cloneMarketRegionGene(value)
		region = &copy
		centre = value.DefaultState
	} else {
		centre = asChromosome(seed)
		if options.MarketRegionEnabled {
			features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
			for _, id := range MarketRegionFeatureIDs {
				features = append(features, MarketRegionFeature{ID: id, Window: 2})
			}
			global := normalizeChromosome(centre, marketRegionGlobalOptions(options))
			value := MarketRegionGene{
				SchemaVersion: MarketRegionSchemaVersion,
				Global:        global,
				DefaultState:  marketRegionStateChromosome(centre),
				Features:      features,
			}
			region = &value
			centre = value.DefaultState
		}
	}
	s := &layeredGridScheduler{
		options: options, region: region, centre: normalizeChromosome(centre, options),
		axisIndex: make(map[string]int), firstPending: true,
		localPercent: normalizeLayeredLocalPercent(localPercent),
	}
	for _, key := range layeredFieldOrder {
		if chromosomeFieldEvolves(key, options) {
			s.addCoreAxis(key, chromosomeValue(s.centre, key))
		}
	}
	if region != nil {
		for _, feature := range region.Features {
			s.addIntegerAxis("market_window:"+feature.ID, 2, int64(options.MarketRegionMaxWindow), int64(feature.Window), false)
			s.addIntegerAxis("market_count:"+feature.ID, 0, int64(options.MarketRegionMaxThresholds), int64(len(feature.ThresholdRanks)), false)
			s.addIntegerAxis("market_combo:"+feature.ID, 0, math.MaxInt64, int64(max(0, feature.ThresholdRankOffset)), true)
		}
		s.addPackAxes(region.Packs)
	}
	return s
}

func restoreLayeredGridScheduler(raw []byte, options GeneOptions) (*layeredGridScheduler, error) {
	var checkpoint layeredGridCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.Version != layeredGridCheckpointVersion {
		return nil, strconv.ErrSyntax
	}
	options = NormalizeGeneOptions(options)
	s := &layeredGridScheduler{
		options: options, region: checkpoint.Region, centre: checkpoint.Centre,
		axes: append([]layeredAxis(nil), checkpoint.Axes...), slice: checkpoint.Slice,
		firstPending: checkpoint.FirstPending, expandAxis: checkpoint.ExpandAxis,
		expandUpper: checkpoint.ExpandUpper, globalCursor: checkpoint.GlobalCursor,
		issued: checkpoint.Issued, duplicateSkips: checkpoint.DuplicateSkips,
		localPercent: normalizeLayeredLocalPercent(checkpoint.LocalPercent),
		axisIndex:    make(map[string]int, len(checkpoint.Axes)),
	}
	if checkpoint.Region != nil {
		copy := cloneMarketRegionGene(*checkpoint.Region)
		s.region = &copy
	}
	if checkpoint.PendingCentre != nil {
		copy := *checkpoint.PendingCentre
		s.pendingCentre = &copy
	}
	if checkpoint.PendingRegion != nil {
		copy := cloneMarketRegionGene(*checkpoint.PendingRegion)
		s.pendingRegion = &copy
	}
	for index, axis := range s.axes {
		s.axisIndex[axis.Key] = index
	}
	return s, nil
}

func (s *layeredGridScheduler) Checkpoint() ([]byte, error) {
	var region *MarketRegionGene
	if s.region != nil {
		copy := cloneMarketRegionGene(*s.region)
		region = &copy
	}
	return json.Marshal(layeredGridCheckpoint{
		Version: layeredGridCheckpointVersion, Options: s.options, Region: region,
		Centre: s.centre, Axes: s.axes, Slice: s.slice, FirstPending: s.firstPending,
		ExpandAxis: s.expandAxis, ExpandUpper: s.expandUpper,
		GlobalCursor: s.globalCursor, Issued: s.issued, DuplicateSkips: s.duplicateSkips,
		LocalPercent: s.localPercent, PendingCentre: s.pendingCentre, PendingRegion: s.pendingRegion,
	})
}

var layeredFieldOrder = []string{
	"micro_reserve_pct", "beta", "gamma", "w_mean", "w_momentum", "w_breakout",
	"dust_usd", "rebalance_threshold", "force_empty_threshold", "force_full_threshold",
	"wedge_delta_threshold", "wedge_vol_ratio_threshold", "macro_bear_multiplier",
	"macro_bull_multiplier", "extra_deploy_pct", "soft_release_months", "soft_release_pct", "hard_release_max_pct",
}

var marketPackFieldOrder = []string{"gamma", "w_mean", "w_momentum", "w_breakout", "force_empty_threshold", "force_full_threshold"}

func (s *layeredGridScheduler) addCoreAxis(key string, value float64) {
	bound := quant.HardBounds[key]
	step := coreSearchStep
	if key == "soft_release_months" {
		step = 1
	}
	minimum := int64(math.Ceil(bound.Min / step))
	maximum := int64(math.Floor(bound.Max / step))
	centre := int64(math.Round(value / step))
	s.addIntegerAxis("core:"+key, minimum, maximum, centre, false)
}

func (s *layeredGridScheduler) addIntegerAxis(key string, minimum, maximum, centre int64, unbounded bool) {
	if _, exists := s.axisIndex[key]; exists {
		return
	}
	if centre < minimum {
		centre = minimum
	}
	if !unbounded && centre > maximum {
		centre = maximum
	}
	s.axisIndex[key] = len(s.axes)
	s.axes = append(s.axes, layeredAxis{
		Key: key, Minimum: minimum, Maximum: maximum, Centre: centre,
		Lower: centre, Upper: centre, Unbounded: unbounded,
	})
}

func (s *layeredGridScheduler) addPackAxes(packs []MarketRegionPack) {
	for _, pack := range packs {
		for _, field := range marketPackFieldOrder {
			key := "market_pack:" + field + ":" + pack.Key
			s.addCoreAxisWithKey(key, chromosomeValue(pack.Chromosome, field), field)
		}
	}
}

func (s *layeredGridScheduler) addCoreAxisWithKey(key string, value float64, boundKey string) {
	if _, exists := s.axisIndex[key]; exists {
		return
	}
	bound := quant.HardBounds[boundKey]
	minimum := int64(math.Ceil(bound.Min / coreSearchStep))
	maximum := int64(math.Floor(bound.Max / coreSearchStep))
	centre := int64(math.Round(value / coreSearchStep))
	s.addIntegerAxis(key, minimum, maximum, centre, false)
}

func (s *layeredGridScheduler) Next() Gene {
	if len(s.axes) == 0 {
		return s.withRegionCentre(s.centre, s.region)
	}
	for {
		useLocal := s.localPercent == 100 || (s.localPercent > 0 && int(s.issued%100) < s.localPercent)
		s.issued++
		var gene Gene
		var valid bool
		if useLocal {
			gene, valid = s.nextLocal()
		} else {
			gene, valid = s.nextGlobal()
		}
		if valid {
			return gene
		}
	}
}

func (s *layeredGridScheduler) nextLocal() (Gene, bool) {
	for {
		if s.firstPending {
			s.firstPending = false
			coordinates := make([]int64, len(s.axes))
			for i := range s.axes {
				coordinates[i] = s.axes[i].Centre
			}
			return s.fromCoordinates(coordinates)
		}
		if s.slice.Total == 0 || s.slice.Cursor >= s.slice.Total {
			if s.pendingCentre != nil {
				s.applyPendingCentre()
				continue
			}
			if !s.prepareNextSlice() {
				return s.nextGlobal()
			}
		}
		coordinates := s.sliceCoordinates(s.slice.Cursor)
		s.slice.Cursor++
		if gene, valid := s.fromCoordinates(coordinates); valid {
			return gene, true
		}
	}
}

func (s *layeredGridScheduler) prepareNextSlice() bool {
	if len(s.axes) == 0 {
		return false
	}
	for attempts := 0; attempts < len(s.axes)*2; attempts++ {
		index := s.expandAxis % len(s.axes)
		axis := &s.axes[index]
		expandUpper := s.expandUpper
		s.expandUpper = !s.expandUpper
		if !s.expandUpper {
			s.expandAxis++
		}
		var value int64
		if expandUpper {
			if !axis.Unbounded && axis.Upper >= axis.Maximum {
				continue
			}
			axis.Upper++
			value = axis.Upper
		} else {
			if axis.Lower <= axis.Minimum {
				continue
			}
			axis.Lower--
			value = axis.Lower
		}
		total := uint64(1)
		for i, other := range s.axes {
			if i == index {
				continue
			}
			total = multiplySaturated(total, uint64(other.Upper-other.Lower+1))
		}
		s.slice = layeredSlice{Axis: index, Value: value, Total: total}
		return true
	}
	return false
}

func (s *layeredGridScheduler) sliceCoordinates(cursor uint64) []int64 {
	coordinates := make([]int64, len(s.axes))
	for i := len(s.axes) - 1; i >= 0; i-- {
		axis := s.axes[i]
		if i == s.slice.Axis {
			coordinates[i] = s.slice.Value
			continue
		}
		count := uint64(axis.Upper - axis.Lower + 1)
		coordinates[i] = axis.Lower + int64(cursor%count)
		cursor /= count
	}
	return coordinates
}

func (s *layeredGridScheduler) nextGlobal() (Gene, bool) {
	for {
		cursor := s.globalCursor
		s.globalCursor++
		coordinates := make([]int64, len(s.axes))
		for i, axis := range s.axes {
			// Every global candidate addresses every axis directly. A mixed-radix
			// counter made later axes effectively constant until all earlier
			// products had rolled over, which is why only a few charts moved.
			// A deterministic low-discrepancy grid order spreads the frontier
			// without introducing random sampling.
			if axis.Unbounded {
				coordinates[i] = axis.Minimum + int64(cursor)*int64(2*(i%31)+1)
				continue
			}
			count := uint64(axis.Maximum - axis.Minimum + 1)
			coordinates[i] = axis.Minimum + int64(radicalGridIndex(cursor+1, globalPrime(i), count))
		}
		s.generateLegalForcePairs(coordinates, cursor)
		if gene, valid := s.fromCoordinates(coordinates); valid {
			return gene, true
		}
	}
}

func (s *layeredGridScheduler) generateLegalForcePairs(coordinates []int64, cursor uint64) {
	for index, axis := range s.axes {
		var emptyKey string
		switch {
		case axis.Key == "core:force_full_threshold":
			emptyKey = "core:force_empty_threshold"
		case strings.HasPrefix(axis.Key, "market_pack:force_full_threshold:"):
			emptyKey = strings.Replace(axis.Key, "market_pack:force_full_threshold:", "market_pack:force_empty_threshold:", 1)
		default:
			continue
		}
		emptyIndex, exists := s.axisIndex[emptyKey]
		if !exists {
			continue
		}
		minimum := max(axis.Minimum, coordinates[emptyIndex])
		if minimum > axis.Maximum {
			continue
		}
		count := uint64(axis.Maximum - minimum + 1)
		coordinates[index] = minimum + int64(radicalGridIndex(cursor+1, globalPrime(index+7), count))
	}
}

var globalPrimes = [...]uint64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71, 73, 79, 83, 89, 97}

func globalPrime(index int) uint64 {
	return globalPrimes[index%len(globalPrimes)]
}

func radicalGridIndex(sequence, base, count uint64) uint64 {
	if count <= 1 {
		return 0
	}
	factor := 1.0 / float64(base)
	fraction := 0.0
	for sequence > 0 {
		fraction += float64(sequence%base) * factor
		sequence /= base
		factor /= float64(base)
	}
	index := uint64(fraction * float64(count))
	if index >= count {
		return count - 1
	}
	return index
}

func (s *layeredGridScheduler) fromCoordinates(coordinates []int64) (Gene, bool) {
	c := s.centre
	var region *MarketRegionGene
	if s.region != nil {
		copy := cloneMarketRegionGene(*s.region)
		region = &copy
	}
	for i, coordinate := range coordinates {
		key := s.axes[i].Key
		switch {
		case strings.HasPrefix(key, "core:"):
			field := strings.TrimPrefix(key, "core:")
			setChromosomeValue(&c, field, latticeValue(field, coordinate))
		case strings.HasPrefix(key, "market_window:"):
			setMarketFeatureWindow(region, strings.TrimPrefix(key, "market_window:"), int(coordinate))
		case strings.HasPrefix(key, "market_count:"):
			setMarketFeatureThresholdCount(region, strings.TrimPrefix(key, "market_count:"), int(coordinate))
		case strings.HasPrefix(key, "market_combo:"):
			setMarketFeatureCombination(region, strings.TrimPrefix(key, "market_combo:"), coordinate)
		case strings.HasPrefix(key, "market_pack:"):
			setMarketPackCoordinate(region, strings.TrimPrefix(key, "market_pack:"), coordinate)
		}
	}
	// Forced-full must be greater than or equal to forced-empty by construction.
	// An illegal combination is not projected into another point and is never
	// returned to reservation or backtesting.
	if c.ForceFullThreshold < c.ForceEmptyThreshold {
		return nil, false
	}
	if region != nil {
		for _, pack := range region.Packs {
			if pack.Chromosome.ForceFullThreshold < pack.Chromosome.ForceEmptyThreshold {
				return nil, false
			}
		}
	}
	return s.withRegionCentre(normalizeChromosome(c, s.options), region), true
}

func (s *layeredGridScheduler) withRegionCentre(c quant.Chromosome, region *MarketRegionGene) Gene {
	if region == nil {
		return c
	}
	copy := cloneMarketRegionGene(*region)
	copy.DefaultState = marketRegionStateChromosome(c)
	return copy
}

func setMarketFeatureWindow(region *MarketRegionGene, id string, window int) {
	if feature := marketFeatureByID(region, id); feature != nil {
		feature.Window = window
		feature.Thresholds = nil
	}
}

func setMarketFeatureThresholdCount(region *MarketRegionGene, id string, count int) {
	if feature := marketFeatureByID(region, id); feature != nil {
		if count < 0 {
			count = 0
		}
		feature.ThresholdRanks = make([]int, count)
		for index := range feature.ThresholdRanks {
			feature.ThresholdRanks[index] = index
		}
		feature.Thresholds = nil
	}
}

func setMarketFeatureCombination(region *MarketRegionGene, id string, combination int64) {
	if feature := marketFeatureByID(region, id); feature != nil {
		if combination < 0 {
			combination = 0
		}
		feature.ThresholdRankOffset = int(min(int64(math.MaxInt), combination))
		feature.Thresholds = nil
	}
}

func marketFeatureByID(region *MarketRegionGene, id string) *MarketRegionFeature {
	if region == nil {
		return nil
	}
	for index := range region.Features {
		if region.Features[index].ID == id {
			return &region.Features[index]
		}
	}
	return nil
}

func setMarketPackCoordinate(region *MarketRegionGene, encoded string, coordinate int64) {
	if region == nil {
		return
	}
	separator := strings.Index(encoded, ":")
	if separator < 1 {
		return
	}
	field := encoded[:separator]
	key := encoded[separator+1:]
	for index := range region.Packs {
		if region.Packs[index].Key == key {
			setChromosomeValue(&region.Packs[index].Chromosome, field, latticeValue(field, coordinate))
			return
		}
	}
}

// ObserveMaterializedStatePackages retains every observed state and adds six
// independent formal axes for each newly observed state. Existing axes and
// their completed bounds are never reset.
func (s *layeredGridScheduler) ObserveMaterializedStatePackages(g Gene) {
	region, ok := isMarketRegionGene(g)
	if !ok {
		return
	}
	copy := cloneMarketRegionGene(region)
	s.region = &copy
	if s.syncPackAxes(copy.Packs) {
		total := uint64(1)
		for _, axis := range s.axes {
			total = multiplySaturated(total, uint64(axis.Upper-axis.Lower+1))
		}
		// A changed observed-state layout changes the executable chromosome
		// dimensions. Enumerate the whole current box once under that new layout
		// before expanding another boundary; durable fingerprints skip only the
		// combinations whose executable result is genuinely unchanged.
		s.slice = layeredSlice{Axis: -1, Total: total}
		s.firstPending = false
	}
}

func (s *layeredGridScheduler) syncPackAxes(packs []MarketRegionPack) bool {
	old := make(map[string]layeredAxis, len(s.axes))
	next := make([]layeredAxis, 0, len(s.axes))
	for _, axis := range s.axes {
		old[axis.Key] = axis
		if !strings.HasPrefix(axis.Key, "market_pack:") {
			next = append(next, axis)
		}
	}
	sorted := append([]MarketRegionPack(nil), packs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, pack := range sorted {
		for _, field := range marketPackFieldOrder {
			key := "market_pack:" + field + ":" + pack.Key
			if axis, exists := old[key]; exists {
				next = append(next, axis)
				continue
			}
			bound := quant.HardBounds[field]
			centre := latticeCoordinate(field, chromosomeValue(pack.Chromosome, field))
			next = append(next, layeredAxis{
				Key: key, Minimum: latticeCoordinate(field, bound.Min),
				Maximum: latticeCoordinate(field, bound.Max),
				Centre:  centre, Lower: centre, Upper: centre,
			})
		}
	}
	changed := len(next) != len(s.axes)
	if !changed {
		for index := range next {
			if next[index].Key != s.axes[index].Key {
				changed = true
				break
			}
		}
	}
	s.axes = next
	s.axisIndex = make(map[string]int, len(next))
	for index, axis := range next {
		s.axisIndex[axis.Key] = index
	}
	if len(s.axes) > 0 {
		s.expandAxis %= len(s.axes)
	}
	return changed
}

// Recenter moves the local box to a newly discovered champion while retaining
// each axis's explored radius. Durable reservations prevent overlap from being
// evaluated again.
func (s *layeredGridScheduler) Recenter(g Gene) {
	var c quant.Chromosome
	var region *MarketRegionGene
	if value, ok := isMarketRegionGene(g); ok {
		copy := cloneMarketRegionGene(value)
		region = &copy
		c = value.DefaultState
	} else {
		c = asChromosome(g)
	}
	c = normalizeChromosome(c, s.options)
	s.pendingCentre = &c
	s.pendingRegion = nil
	if region != nil {
		copy := cloneMarketRegionGene(*region)
		s.pendingRegion = &copy
	}
}

func (s *layeredGridScheduler) applyPendingCentre() {
	if s.pendingCentre == nil {
		return
	}
	c := *s.pendingCentre
	region := s.pendingRegion
	for index := range s.axes {
		axis := &s.axes[index]
		radiusLower := axis.Centre - axis.Lower
		radiusUpper := axis.Upper - axis.Centre
		centre, ok := coordinateForAxis(axis.Key, c, region)
		if !ok {
			continue
		}
		axis.Centre = centre
		axis.Lower = max(axis.Minimum, centre-radiusLower)
		axis.Upper = centre + radiusUpper
		if !axis.Unbounded {
			axis.Upper = min(axis.Maximum, axis.Upper)
		}
	}
	s.centre = c
	if region != nil {
		copy := cloneMarketRegionGene(*region)
		s.region = &copy
	}
	s.pendingCentre = nil
	s.pendingRegion = nil
	total := uint64(1)
	for _, axis := range s.axes {
		total = multiplySaturated(total, uint64(axis.Upper-axis.Lower+1))
	}
	// The centre moved only after the preceding slice was exhausted. Enumerate
	// the complete shifted box before expanding another boundary.
	s.slice = layeredSlice{Axis: -1, Total: total}
	s.firstPending = false
}

func coordinateForAxis(key string, c quant.Chromosome, region *MarketRegionGene) (int64, bool) {
	switch {
	case strings.HasPrefix(key, "core:"):
		field := strings.TrimPrefix(key, "core:")
		return latticeCoordinate(field, chromosomeValue(c, field)), true
	case strings.HasPrefix(key, "market_window:"):
		feature := marketFeatureByID(region, strings.TrimPrefix(key, "market_window:"))
		if feature != nil {
			return int64(feature.Window), true
		}
	case strings.HasPrefix(key, "market_count:"):
		feature := marketFeatureByID(region, strings.TrimPrefix(key, "market_count:"))
		if feature != nil {
			return int64(len(feature.ThresholdRanks)), true
		}
	case strings.HasPrefix(key, "market_combo:"):
		feature := marketFeatureByID(region, strings.TrimPrefix(key, "market_combo:"))
		if feature != nil {
			return int64(feature.ThresholdRankOffset), true
		}
	case strings.HasPrefix(key, "market_pack:"):
		encoded := strings.TrimPrefix(key, "market_pack:")
		separator := strings.Index(encoded, ":")
		if separator > 0 && region != nil {
			field, stateKey := encoded[:separator], encoded[separator+1:]
			for _, pack := range region.Packs {
				if pack.Key == stateKey {
					return latticeCoordinate(field, chromosomeValue(pack.Chromosome, field)), true
				}
			}
		}
	}
	return 0, false
}

func (s *layeredGridScheduler) Bounds() []LayeredAxisStatus {
	out := make([]LayeredAxisStatus, 0, len(s.axes))
	for _, axis := range s.axes {
		status := LayeredAxisStatus{
			Key: axis.Key, Centre: displayAxisCoordinate(axis.Key, axis.Centre),
			Lower: displayAxisCoordinate(axis.Key, axis.Lower),
			Upper: displayAxisCoordinate(axis.Key, axis.Upper),
			Step:  axisDisplayStep(axis.Key),
		}
		if !axis.Unbounded {
			minimum := displayAxisCoordinate(axis.Key, axis.Minimum)
			maximum := displayAxisCoordinate(axis.Key, axis.Maximum)
			status.Minimum = &minimum
			status.Maximum = &maximum
		}
		out = append(out, status)
	}
	return out
}

type LayeredAxisStatus struct {
	Key     string   `json:"key"`
	Centre  float64  `json:"centre"`
	Lower   float64  `json:"lower"`
	Upper   float64  `json:"upper"`
	Step    float64  `json:"step"`
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}

type LayeredSearchStatus struct {
	Axes                  []LayeredAxisStatus `json:"axes"`
	NextAxis              string              `json:"next_axis,omitempty"`
	NextSide              string              `json:"next_side,omitempty"`
	LocalPercent          int                 `json:"local_percent"`
	Issued                uint64              `json:"issued"`
	GlobalCursor          uint64              `json:"global_cursor"`
	DuplicateSkips        uint64              `json:"duplicate_skips"`
	CurrentSliceRemaining uint64              `json:"current_slice_remaining"`
}

func (s *layeredGridScheduler) Status() LayeredSearchStatus {
	status := LayeredSearchStatus{
		Axes: s.Bounds(), LocalPercent: s.localPercent,
		Issued: s.issued, GlobalCursor: s.globalCursor, DuplicateSkips: s.duplicateSkips,
	}
	if len(s.axes) > 0 {
		status.NextAxis = s.axes[s.expandAxis%len(s.axes)].Key
		if s.expandUpper {
			status.NextSide = "上界"
		} else {
			status.NextSide = "下界"
		}
	}
	if s.slice.Total > s.slice.Cursor {
		status.CurrentSliceRemaining = s.slice.Total - s.slice.Cursor
	}
	return status
}

func (s *layeredGridScheduler) RecordDuplicate() {
	s.duplicateSkips++
}

func cloneMarketRegionGene(value MarketRegionGene) MarketRegionGene {
	copy := value
	copy.Features = append([]MarketRegionFeature(nil), value.Features...)
	for index := range copy.Features {
		copy.Features[index].Thresholds = append([]float64(nil), value.Features[index].Thresholds...)
		copy.Features[index].ThresholdRanks = append([]int(nil), value.Features[index].ThresholdRanks...)
	}
	copy.Packs = append([]MarketRegionPack(nil), value.Packs...)
	return copy
}

func latticeCoordinate(key string, value float64) int64 {
	step := coreSearchStep
	if key == "soft_release_months" {
		step = 1
	}
	return int64(math.Round(value / step))
}

func latticeValue(key string, coordinate int64) float64 {
	step := coreSearchStep
	if key == "soft_release_months" {
		step = 1
	}
	return float64(coordinate) * step
}

func axisDisplayStep(key string) float64 {
	switch {
	case strings.HasPrefix(key, "market_window:"), strings.HasPrefix(key, "market_count:"), strings.HasPrefix(key, "market_combo:"):
		return 1
	case strings.Contains(key, "soft_release_months"):
		return 1
	default:
		return coreSearchStep
	}
}

func displayAxisCoordinate(key string, coordinate int64) float64 {
	switch {
	case strings.HasPrefix(key, "core:"):
		return latticeValue(strings.TrimPrefix(key, "core:"), coordinate)
	case strings.HasPrefix(key, "market_pack:"):
		encoded := strings.TrimPrefix(key, "market_pack:")
		field := encoded[:strings.Index(encoded, ":")]
		return latticeValue(field, coordinate)
	default:
		return float64(coordinate)
	}
}

func chromosomeValue(c quant.Chromosome, key string) float64 {
	switch key {
	case "micro_reserve_pct":
		return c.MicroReservePct
	case "beta":
		return c.Beta
	case "gamma":
		return c.Gamma
	case "w_mean":
		return c.WMean
	case "w_momentum":
		return c.WMomentum
	case "w_breakout":
		return c.WBreakout
	case "dust_usd":
		return c.DustUSD
	case "rebalance_threshold":
		return c.RebalanceThreshold
	case "force_full_threshold":
		return c.ForceFullThreshold
	case "force_empty_threshold":
		return c.ForceEmptyThreshold
	case "wedge_delta_threshold":
		return c.WedgeDeltaThreshold
	case "wedge_vol_ratio_threshold":
		return c.WedgeVolRatioThreshold
	case "macro_bear_multiplier":
		return c.MacroBearMultiplier
	case "macro_bull_multiplier":
		return c.MacroBullMultiplier
	case "extra_deploy_pct":
		return c.ExtraDeployPct
	case "soft_release_months":
		return float64(c.SoftReleaseMonths)
	case "soft_release_pct":
		return c.SoftReleasePct
	case "hard_release_max_pct":
		return c.HardReleaseMaxPct
	}
	return 0
}

func setChromosomeValue(c *quant.Chromosome, key string, value float64) {
	switch key {
	case "micro_reserve_pct":
		c.MicroReservePct = value
	case "beta":
		c.Beta = value
	case "gamma":
		c.Gamma = value
	case "w_mean":
		c.WMean = value
	case "w_momentum":
		c.WMomentum = value
	case "w_breakout":
		c.WBreakout = value
	case "dust_usd":
		c.DustUSD = value
	case "rebalance_threshold":
		c.RebalanceThreshold = value
	case "force_full_threshold":
		c.ForceFullThreshold = value
	case "force_empty_threshold":
		c.ForceEmptyThreshold = value
	case "wedge_delta_threshold":
		c.WedgeDeltaThreshold = value
	case "wedge_vol_ratio_threshold":
		c.WedgeVolRatioThreshold = value
	case "macro_bear_multiplier":
		c.MacroBearMultiplier = value
	case "macro_bull_multiplier":
		c.MacroBullMultiplier = value
	case "extra_deploy_pct":
		c.ExtraDeployPct = value
	case "soft_release_months":
		c.SoftReleaseMonths = int(math.Round(value))
	case "soft_release_pct":
		c.SoftReleasePct = value
	case "hard_release_max_pct":
		c.HardReleaseMaxPct = value
	}
}

func layeredThresholdRanks(maximum int, cursor uint64) ([]int, int) {
	if maximum <= 0 {
		return nil, 0
	}
	count := 1 + int(cursor%uint64(maximum))
	offset := int(cursor / uint64(maximum))
	ranks := make([]int, count)
	for index := range ranks {
		ranks[index] = index
	}
	return ranks, offset
}

func multiplySaturated(left, right uint64) uint64 {
	if right != 0 && left > math.MaxUint64/right {
		return math.MaxUint64
	}
	return left * right
}

func parseAxisNumber(value string) int {
	result, _ := strconv.Atoi(value)
	return result
}

func sortedPackKeys(packs []MarketRegionPack) []string {
	keys := make([]string, 0, len(packs))
	for _, pack := range packs {
		keys = append(keys, pack.Key)
	}
	sort.Strings(keys)
	return keys
}
