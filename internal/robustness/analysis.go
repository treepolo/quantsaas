package robustness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

func Analyze(space ParameterSpace, points []EvaluationPoint, centerID string, radii []int, metric MetricName) (AnalysisResult, error) {
	if err := ValidateSpace(space); err != nil {
		return AnalysisResult{}, err
	}
	if !ValidMetric(metric) {
		return AnalysisResult{}, ErrInvalidPoint
	}
	if len(points) == 0 {
		return AnalysisResult{}, ErrInvalidPoint
	}
	byKey := make(map[string]*EvaluationPoint, len(points))
	actual := make([]EvaluationPoint, 0, len(points))
	predictions := make([]EvaluationPoint, 0)
	for index := range points {
		point := points[index]
		if len(point.Coordinates) != len(space.Axes) {
			return AnalysisResult{}, ErrInvalidPoint
		}
		key := CoordinateKey(point.Coordinates)
		if point.ID == "" {
			point.ID = key
		}
		if point.Kind == PointPredicted || point.Kind == PointProposed {
			predictions = append(predictions, point)
			continue
		}
		if point.Metrics == nil {
			point.State = PointUnknown
		} else if point.Metrics.Qualified {
			point.State = PointQualified
		} else {
			point.State = PointUnqualified
		}
		copy := point
		byKey[key] = &copy
		actual = append(actual, copy)
	}
	if len(actual) == 0 {
		return AnalysisResult{}, ErrInvalidPoint
	}
	sortPoints(actual)
	center := byKey[centerID]
	if center == nil && centerID != "" {
		for index := range actual {
			if actual[index].ID == centerID {
				center = &actual[index]
				break
			}
		}
	}
	if center == nil {
		center = &actual[0]
		centerID = center.ID
	}

	missing := missingCoordinates(space, byKey)
	scales := make([]ScaleStatistic, 0, len(radii))
	seenRadius := map[int]bool{}
	for _, radius := range radii {
		if radius <= 0 || seenRadius[radius] {
			continue
		}
		seenRadius[radius] = true
		scales = append(scales, scaleStatistic(space, byKey, *center, radius, metric))
	}

	regions := connectedRegions(space, byKey, metric)
	resultPoints := append(actual, predictions...)
	sortPoints(resultPoints)
	pointSetHash, err := hashPointSet(actual)
	if err != nil {
		return AnalysisResult{}, err
	}
	return AnalysisResult{
		AnalysisVersion: AnalysisVersion, ConnectivityVersion: ConnectivityVersion,
		DistanceVersion: DistanceVersion, FrontierVersion: FrontierVersion, CenterVersion: CenterVersion,
		Metric: metric, CenterPointID: centerID, Points: resultPoints, Scales: scales,
		Regions: regions, MissingCoordinates: missing, ObservedPointSetHash: pointSetHash,
	}, nil
}

func scaleStatistic(space ParameterSpace, points map[string]*EvaluationPoint, center EvaluationPoint, radius int, metric MetricName) ScaleStatistic {
	coordinates := boxCoordinates(space, center.Coordinates, radius)
	values := make([]float64, 0, len(coordinates))
	qualified := 0
	for _, coordinate := range coordinates {
		point := points[CoordinateKey(coordinate)]
		if point == nil || point.Metrics == nil || point.State == PointUnknown {
			continue
		}
		values = append(values, point.Metrics.Value(metric))
		if point.State == PointQualified {
			qualified++
		}
	}
	stat := ScaleStatistic{
		Radius: radius, ExpectedPoints: len(coordinates), ObservedPoints: len(values),
		UnknownPoints: len(coordinates) - len(values), QualifiedPoints: qualified,
		Complete: len(values) == len(coordinates),
	}
	if len(values) == 0 {
		return stat
	}
	stat.QualificationRatio = float64(qualified) / float64(len(values))
	stat.AreaRatio = stat.QualificationRatio
	stat.Mean, stat.Median, stat.StandardDeviation = distribution(values)
	if center.Metrics != nil {
		centerValue := center.Metrics.Value(metric)
		if math.Abs(stat.Mean) > 1e-15 {
			value := centerValue / stat.Mean
			stat.CenterToMean = &value
		}
		if math.Abs(stat.Median) > 1e-15 {
			value := centerValue / stat.Median
			stat.CenterToMedian = &value
		}
	}
	return stat
}

func connectedRegions(space ParameterSpace, points map[string]*EvaluationPoint, metric MetricName) []ConnectedRegion {
	qualified := map[string]*EvaluationPoint{}
	for key, point := range points {
		if point.Kind == PointActual && point.State == PointQualified && point.Metrics != nil {
			qualified[key] = point
		}
	}
	keys := sortedKeys(qualified)
	visited := map[string]bool{}
	regions := make([]ConnectedRegion, 0)
	for _, start := range keys {
		if visited[start] {
			continue
		}
		queue := []string{start}
		visited[start] = true
		component := make([]string, 0)
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			component = append(component, current)
			for _, neighbor := range axisNeighbors(space, qualified[current].Coordinates) {
				key := CoordinateKey(neighbor)
				if qualified[key] == nil || visited[key] {
					continue
				}
				visited[key] = true
				queue = append(queue, key)
			}
		}
		sort.Strings(component)
		region := buildRegion(len(regions)+1, space, points, qualified, component, metric)
		regions = append(regions, region)
	}
	return regions
}

func buildRegion(number int, space ParameterSpace, all map[string]*EvaluationPoint, qualified map[string]*EvaluationPoint, component []string, metric MetricName) ConnectedRegion {
	geometries := make([]PointGeometry, 0, len(component))
	geometryByID := map[string]PointGeometry{}
	for _, key := range component {
		geometry := pointGeometry(space, all, qualified, component, *qualified[key])
		geometries = append(geometries, geometry)
		geometryByID[qualified[key].ID] = geometry
	}
	frontier := paretoFrontier(component, qualified, geometryByID, metric)
	centers := selectCenters(frontier, qualified, geometryByID)
	proposals := roleProposals(frontier, centers, qualified, geometryByID)
	pointIDs := make([]string, 0, len(component))
	for _, key := range component {
		pointIDs = append(pointIDs, qualified[key].ID)
	}
	sort.Strings(pointIDs)
	return ConnectedRegion{
		ID: fmt.Sprintf("region-%d", number), PointIDs: pointIDs, Geometries: geometries,
		FrontierIDs: frontier, CenterIDs: centers, Proposals: proposals,
	}
}

func pointGeometry(space ParameterSpace, all map[string]*EvaluationPoint, qualified map[string]*EvaluationPoint, component []string, point EvaluationPoint) PointGeometry {
	directions := make([]DirectionTolerance, 0, len(space.Axes)*2)
	failureDepth := math.MaxInt
	failureExact := false
	truncation := map[string]bool{}
	legalStops := 0
	for dimension, axis := range space.Axes {
		for _, direction := range []int{-1, 1} {
			steps := 0
			cursor := append([]int(nil), point.Coordinates...)
			stop := StopResearchBoundary
			for {
				cursor[dimension] += direction
				if cursor[dimension] < axis.StudyStart || cursor[dimension] > axis.StudyEnd {
					if (cursor[dimension] < axis.StudyStart && axis.Values[axis.StudyStart] <= axis.LegalMin+1e-9) ||
						(cursor[dimension] > axis.StudyEnd && axis.Values[axis.StudyEnd] >= axis.LegalMax-1e-9) {
						stop = StopLegalBoundary
						legalStops++
					} else {
						stop = StopResearchBoundary
						truncation[fmt.Sprintf("%s:%d:%s", axis.Name, direction, stop)] = true
					}
					break
				}
				candidate := all[CoordinateKey(cursor)]
				if candidate == nil || candidate.State == PointUnknown || candidate.Metrics == nil {
					stop = StopUnknownGap
					truncation[fmt.Sprintf("%s:%d:%s", axis.Name, direction, stop)] = true
					break
				}
				if candidate.State == PointUnqualified {
					stop = StopObservedFailure
					depth := steps + 1
					if depth < failureDepth {
						failureDepth = depth
					}
					failureExact = true
					break
				}
				steps++
			}
			name := "up"
			if direction < 0 {
				name = "down"
			}
			directions = append(directions, DirectionTolerance{Axis: axis.Name, Direction: name, Steps: steps, Stop: stop})
		}
	}
	if failureDepth == math.MaxInt {
		failureDepth = 0
	}
	boxRadius, boxExact, boxReasons := guaranteedBox(space, all, point)
	for _, reason := range boxReasons {
		truncation[reason] = true
	}
	quality, stability, dispersion, neighborhoodComplete := neighborhoodObjectives(space, all, point, metricForGeometry(point))
	if !neighborhoodComplete {
		truncation["incomplete_multiscale_neighborhood"] = true
	}
	completeness := "formal_bilateral"
	if len(truncation) > 0 {
		completeness = "provisional"
	} else if legalStops > 0 {
		completeness = "formal_unilateral"
	}
	reasons := sortedBoolKeys(truncation)
	return PointGeometry{
		PointID: point.ID, Directions: directions, AxisFailureDepth: failureDepth,
		AxisFailureExact: failureExact, GuaranteedBoxRadius: boxRadius, GuaranteedBoxExact: boxExact,
		NeighborhoodQuality: quality, NeighborhoodStability: stability, NeighborhoodDispersion: dispersion,
		MedoidCost:   medoidCost(space, qualified, component, CoordinateKey(point.Coordinates)),
		Completeness: completeness, TruncationReasons: reasons,
	}
}

func metricForGeometry(_ EvaluationPoint) MetricName {
	// Formal neighborhood dispersion is intentionally tied to the primary
	// performance objective, independent of the UI Z-axis selection.
	return MetricLogFinalNAVRatio
}

func neighborhoodObjectives(space ParameterSpace, points map[string]*EvaluationPoint, center EvaluationPoint, metric MetricName) (float64, float64, float64, bool) {
	qualification := make([]float64, 0, 3)
	allValues := make([]float64, 0)
	complete := true
	for _, radius := range []int{1, 2, 3} {
		coordinates := boxCoordinates(space, center.Coordinates, radius)
		if len(coordinates) == 0 {
			continue
		}
		observed, qualified := 0, 0
		for _, coordinate := range coordinates {
			point := points[CoordinateKey(coordinate)]
			if point == nil || point.Metrics == nil || point.State == PointUnknown {
				complete = false
				continue
			}
			observed++
			if point.State == PointQualified {
				qualified++
			}
			allValues = append(allValues, point.Metrics.Value(metric))
		}
		if observed == 0 {
			complete = false
			continue
		}
		qualification = append(qualification, float64(qualified)/float64(observed))
	}
	if len(qualification) == 0 {
		return 0, 0, 0, false
	}
	quality, _, qualificationDeviation := distribution(qualification)
	stability := 1 / (1 + qualificationDeviation)
	_, _, dispersion := distribution(allValues)
	return quality, stability, dispersion, complete
}

func guaranteedBox(space ParameterSpace, all map[string]*EvaluationPoint, point EvaluationPoint) (int, bool, []string) {
	maxRadius := 0
	for dimension, axis := range space.Axes {
		distance := max(point.Coordinates[dimension]-axis.StudyStart, axis.StudyEnd-point.Coordinates[dimension])
		if distance > maxRadius {
			maxRadius = distance
		}
	}
	reasons := map[string]bool{}
	lastGood := 0
	for radius := 1; radius <= maxRadius+1; radius++ {
		coordinates := boxCoordinates(space, point.Coordinates, radius)
		complete := true
		for _, coordinate := range coordinates {
			candidate := all[CoordinateKey(coordinate)]
			if candidate == nil || candidate.State == PointUnknown || candidate.Metrics == nil {
				complete = false
				reasons["unknown_gap"] = true
				break
			}
			if candidate.State != PointQualified {
				return lastGood, true, sortedBoolKeys(reasons)
			}
		}
		if !complete {
			return lastGood, false, sortedBoolKeys(reasons)
		}
		lastGood = radius
		clipped := false
		for dimension, axis := range space.Axes {
			if point.Coordinates[dimension]-radius <= axis.StudyStart && axis.Values[axis.StudyStart] > axis.LegalMin+1e-9 {
				clipped = true
				reasons[axis.Name+":research_lower"] = true
			}
			if point.Coordinates[dimension]+radius >= axis.StudyEnd && axis.Values[axis.StudyEnd] < axis.LegalMax-1e-9 {
				clipped = true
				reasons[axis.Name+":research_upper"] = true
			}
		}
		if clipped || radius > maxRadius {
			return lastGood, false, sortedBoolKeys(reasons)
		}
	}
	return lastGood, true, sortedBoolKeys(reasons)
}

func paretoFrontier(component []string, points map[string]*EvaluationPoint, geometries map[string]PointGeometry, metric MetricName) []string {
	frontier := make([]string, 0)
	for _, key := range component {
		candidate := points[key]
		geometry := geometries[candidate.ID]
		dominated := false
		for _, otherKey := range component {
			if key == otherKey {
				continue
			}
			other := points[otherKey]
			otherGeometry := geometries[other.ID]
			if dominates(*other, otherGeometry, *candidate, geometry, metric) {
				dominated = true
				break
			}
		}
		if !dominated {
			frontier = append(frontier, candidate.ID)
		}
	}
	sort.Strings(frontier)
	return frontier
}

func dominates(a EvaluationPoint, ag PointGeometry, b EvaluationPoint, bg PointGeometry, metric MetricName) bool {
	_ = metric
	if a.Metrics == nil || b.Metrics == nil {
		return false
	}
	if ag.Completeness == "provisional" && bg.Completeness != "provisional" {
		return false
	}
	av := []float64{a.Metrics.LogFinalNAVRatio, a.Metrics.LogDrawdownResidualRatio, float64(ag.GuaranteedBoxRadius), ag.NeighborhoodQuality, ag.NeighborhoodStability, -ag.NeighborhoodDispersion}
	bv := []float64{b.Metrics.LogFinalNAVRatio, b.Metrics.LogDrawdownResidualRatio, float64(bg.GuaranteedBoxRadius), bg.NeighborhoodQuality, bg.NeighborhoodStability, -bg.NeighborhoodDispersion}
	better := false
	for index := range av {
		if av[index] < bv[index]-1e-12 {
			return false
		}
		if av[index] > bv[index]+1e-12 {
			better = true
		}
	}
	return better
}

func selectCenters(frontier []string, points map[string]*EvaluationPoint, geometry map[string]PointGeometry) []string {
	if len(frontier) == 0 {
		return nil
	}
	pools := []string{"formal_bilateral", "formal_unilateral", "provisional"}
	candidates := append([]string(nil), frontier...)
	selectedPool := ""
	for _, pool := range pools {
		for _, id := range candidates {
			if geometry[id].Completeness == pool {
				selectedPool = pool
				break
			}
		}
		if selectedPool != "" {
			break
		}
	}
	filtered := candidates[:0]
	for _, id := range candidates {
		if geometry[id].Completeness == selectedPool {
			filtered = append(filtered, id)
		}
	}
	maxBox := -1
	for _, id := range filtered {
		maxBox = max(maxBox, geometry[id].GuaranteedBoxRadius)
	}
	filtered = keepIDs(filtered, func(id string) bool { return geometry[id].GuaranteedBoxRadius == maxBox })
	maxDepth := -1
	for _, id := range filtered {
		maxDepth = max(maxDepth, geometry[id].AxisFailureDepth)
	}
	filtered = keepIDs(filtered, func(id string) bool { return geometry[id].AxisFailureDepth == maxDepth })
	minMedoid := math.MaxInt
	for _, id := range filtered {
		minMedoid = min(minMedoid, geometry[id].MedoidCost)
	}
	filtered = keepIDs(filtered, func(id string) bool { return geometry[id].MedoidCost == minMedoid })
	result := make([]string, 0, len(filtered))
	for _, id := range filtered {
		dominated := false
		for _, otherID := range filtered {
			if id == otherID {
				continue
			}
			a, b := pointsByID(points, otherID), pointsByID(points, id)
			if a != nil && b != nil && a.Metrics.LogFinalNAVRatio >= b.Metrics.LogFinalNAVRatio && a.Metrics.LogDrawdownResidualRatio >= b.Metrics.LogDrawdownResidualRatio &&
				(a.Metrics.LogFinalNAVRatio > b.Metrics.LogFinalNAVRatio || a.Metrics.LogDrawdownResidualRatio > b.Metrics.LogDrawdownResidualRatio) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func roleProposals(frontier, centers []string, points map[string]*EvaluationPoint, geometry map[string]PointGeometry) []RoleProposal {
	roles := map[string]map[string]bool{}
	for _, id := range centers {
		addRole(roles, id, "robust_center")
	}
	if len(frontier) > 0 {
		for _, id := range tiedBest(frontier, points, geometry, "return") {
			addRole(roles, id, "return_leader")
		}
		for _, id := range tiedBest(frontier, points, geometry, "drawdown") {
			addRole(roles, id, "drawdown_leader")
		}
		for _, id := range tiedBest(frontier, points, geometry, "composite") {
			addRole(roles, id, "composite_leader")
		}
	}
	ids := sortedMapKeys(roles)
	result := make([]RoleProposal, 0, len(ids))
	for _, id := range ids {
		result = append(result, RoleProposal{PointID: id, Roles: sortedBoolKeys(roles[id]), Provisional: geometry[id].Completeness == "provisional"})
	}
	return result
}

func tiedBest(ids []string, points map[string]*EvaluationPoint, geometry map[string]PointGeometry, role string) []string {
	result := []string{}
	best := math.Inf(-1)
	for _, id := range ids {
		point := pointsByID(points, id)
		if point == nil || point.Metrics == nil {
			continue
		}
		value := point.Metrics.LogFinalNAVRatio
		if role == "drawdown" {
			value = point.Metrics.LogDrawdownResidualRatio
		} else if role == "composite" {
			value = point.Metrics.PerformanceDrawdown
		}
		if value > best+1e-12 {
			best, result = value, []string{id}
		} else if math.Abs(value-best) <= 1e-12 {
			result = append(result, id)
		}
	}
	// Apply documented role tie breaks without inventing a weighted score.
	if len(result) <= 1 {
		return result
	}
	return keepBestByTie(result, points, geometry, role)
}

func keepBestByTie(ids []string, points map[string]*EvaluationPoint, geometry map[string]PointGeometry, role string) []string {
	bestSecondary, bestTolerance := math.Inf(-1), -1
	result := []string{}
	for _, id := range ids {
		point := pointsByID(points, id)
		secondary := float64(geometry[id].GuaranteedBoxRadius)
		if role == "return" {
			secondary = point.Metrics.LogDrawdownResidualRatio
		} else if role == "drawdown" {
			secondary = point.Metrics.LogFinalNAVRatio
		}
		tolerance := geometry[id].GuaranteedBoxRadius
		if secondary > bestSecondary+1e-12 || (math.Abs(secondary-bestSecondary) <= 1e-12 && tolerance > bestTolerance) {
			bestSecondary, bestTolerance, result = secondary, tolerance, []string{id}
		} else if math.Abs(secondary-bestSecondary) <= 1e-12 && tolerance == bestTolerance {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func medoidCost(space ParameterSpace, qualified map[string]*EvaluationPoint, component []string, start string) int {
	distances := map[string]int{start: 0}
	queue := []string{start}
	allowed := map[string]bool{}
	for _, key := range component {
		allowed[key] = true
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, coordinate := range axisNeighbors(space, qualified[current].Coordinates) {
			key := CoordinateKey(coordinate)
			if !allowed[key] {
				continue
			}
			if _, exists := distances[key]; exists {
				continue
			}
			distances[key] = distances[current] + 1
			queue = append(queue, key)
		}
	}
	cost := 0
	for _, distance := range distances {
		cost += distance
	}
	return cost
}

func missingCoordinates(space ParameterSpace, points map[string]*EvaluationPoint) [][]int {
	all, _ := Enumerate(space)
	missing := make([][]int, 0)
	for _, point := range all {
		observed := points[CoordinateKey(point.Coordinates)]
		if observed == nil || observed.State == PointUnknown || observed.Metrics == nil {
			missing = append(missing, append([]int(nil), point.Coordinates...))
		}
	}
	return missing
}

func boxCoordinates(space ParameterSpace, center []int, radius int) [][]int {
	result := [][]int{{}}
	for dimension, axis := range space.Axes {
		lower := max(axis.StudyStart, center[dimension]-radius)
		upper := min(axis.StudyEnd, center[dimension]+radius)
		next := make([][]int, 0, len(result)*(upper-lower+1))
		for _, prefix := range result {
			for value := lower; value <= upper; value++ {
				next = append(next, append(append([]int(nil), prefix...), value))
			}
		}
		result = next
	}
	filtered := result[:0]
	for _, coordinate := range result {
		if !isExcluded(space, coordinate) {
			filtered = append(filtered, coordinate)
		}
	}
	return filtered
}

func axisNeighbors(space ParameterSpace, coordinates []int) [][]int {
	result := make([][]int, 0, len(space.Axes)*2)
	for dimension, axis := range space.Axes {
		for _, delta := range []int{-1, 1} {
			value := coordinates[dimension] + delta
			if value < axis.StudyStart || value > axis.StudyEnd {
				continue
			}
			neighbor := append([]int(nil), coordinates...)
			neighbor[dimension] = value
			if !isExcluded(space, neighbor) {
				result = append(result, neighbor)
			}
		}
	}
	return result
}

func distribution(values []float64) (float64, float64, float64) {
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	mean := 0.0
	for _, value := range copyValues {
		mean += value
	}
	mean /= float64(len(copyValues))
	median := copyValues[len(copyValues)/2]
	if len(copyValues)%2 == 0 {
		median = (copyValues[len(copyValues)/2-1] + copyValues[len(copyValues)/2]) / 2
	}
	variance := 0.0
	for _, value := range copyValues {
		variance += (value - mean) * (value - mean)
	}
	variance /= float64(len(copyValues))
	return mean, median, math.Sqrt(variance)
}

func hashPointSet(points []EvaluationPoint) (string, error) {
	type identity struct {
		ID          string           `json:"id"`
		Coordinates []int            `json:"coordinates"`
		Metrics     *RelativeMetrics `json:"metrics"`
		ResultID    uint             `json:"backtest_result_id"`
	}
	rows := make([]identity, 0, len(points))
	for _, point := range points {
		rows = append(rows, identity{point.ID, point.Coordinates, point.Metrics, point.BacktestResultID})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func sortPoints(points []EvaluationPoint) {
	sort.SliceStable(points, func(i, j int) bool {
		if points[i].Kind != points[j].Kind {
			return points[i].Kind < points[j].Kind
		}
		return points[i].ID < points[j].ID
	})
}

func pointsByID(points map[string]*EvaluationPoint, id string) *EvaluationPoint {
	for _, point := range points {
		if point.ID == id {
			return point
		}
	}
	return nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]map[string]bool) []string { return sortedKeys(values) }
func sortedBoolKeys(values map[string]bool) []string           { return sortedKeys(values) }

func keepIDs(values []string, predicate func(string) bool) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if predicate(value) {
			result = append(result, value)
		}
	}
	return result
}

func addRole(roles map[string]map[string]bool, id, role string) {
	if roles[id] == nil {
		roles[id] = map[string]bool{}
	}
	roles[id][role] = true
}
