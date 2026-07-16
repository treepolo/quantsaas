package perturbation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	core "quantsaas/internal/perturbation"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/saas/marketdata"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const estimatedBytesPerBar int64 = 224

type Service struct {
	db                *gorm.DB
	computeTasks      *computetask.Service
	parameterResearch *parameterresearchsvc.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service, parameterResearch *parameterresearchsvc.Service) *Service {
	return &Service{db: db, computeTasks: tasks, parameterResearch: parameterResearch}
}

func (s *Service) SetComputeTasks(tasks *computetask.Service) { s.computeTasks = tasks }
func (s *Service) SetParameterResearch(service *parameterresearchsvc.Service) {
	s.parameterResearch = service
}

func (s *Service) Sources(ctx context.Context, userID uint) ([]SourceDescriptor, error) {
	if s == nil || s.db == nil || userID == 0 {
		return nil, ErrNotFound
	}
	var instruments []saasstore.ResearchInstrument
	if err := s.db.WithContext(ctx).Where("enabled = ? AND internal_only = ? AND data_source <> ?", true, false, marketdata.DataSourceFRED).Order("sort_order ASC,id ASC").Find(&instruments).Error; err != nil {
		return nil, err
	}
	result := make([]SourceDescriptor, 0, len(instruments))
	for _, row := range instruments {
		var intervals []string
		_ = json.Unmarshal(row.SupportedIntervals, &intervals)
		for _, interval := range intervals {
			if row.DataSource == marketdata.DataSourceGenerated {
				var metadata saasstore.DatasetMetadata
				if s.db.WithContext(ctx).Where("instrument_id=? AND data_source=? AND symbol=? AND interval=?", row.ID, row.DataSource, row.Symbol, interval).First(&metadata).Error != nil || metadata.PriceAdjustment != marketdata.PriceAdjustmentGeneratedDailyLeverage {
					continue
				}
			}
			result = append(result, SourceDescriptor{InstrumentID: row.ID, DataSource: row.DataSource, Symbol: row.Symbol, DisplayName: row.DisplayName, Interval: interval, ArtifactKind: sourceArtifactKind(row.DataSource), Immutable: false})
		}
	}
	var versions []saasstore.MarketDataVersion
	if err := s.db.WithContext(ctx).Where("owner_user_id=? AND status=? AND integrity_status=? AND archived_at IS NULL AND artifact_kind <> ? AND has_perturbation_ancestor=?", userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid, marketversion.ArtifactKindLocalPerturbation, false).Order("created_at DESC,id DESC").Find(&versions).Error; err != nil {
		return nil, err
	}
	for _, version := range versions {
		if version.ArtifactKind == marketversion.ArtifactKindSourceSnapshot && version.GeneratorVersion == "p13-source-snapshot-v1" {
			continue
		}
		id := version.InstrumentID
		if version.OutputInstrumentID != nil {
			id = *version.OutputInstrumentID
		}
		result = append(result, SourceDescriptor{InstrumentID: id, DataSource: version.DataSource, Symbol: version.Symbol, DisplayName: version.Symbol, Interval: version.Interval, VersionID: version.ID, ContentHash: version.ContentHash, ArtifactKind: version.ArtifactKind, HasPerturbationAncestor: version.HasPerturbationAncestor, Immutable: true})
	}
	return result, nil
}

type resolvedSource struct {
	descriptor                                                  SourceDescriptor
	bars                                                        []core.Bar
	previous                                                    *float64
	market, timezone, calendarID, calendarVersion, calendarHash string
	sourceVersionID                                             uint
}

func (s *Service) resolveSource(ctx context.Context, userID uint, req SourceRequest) (resolvedSource, error) {
	if req.Interval == "" || req.StartTimeMs <= 0 || req.EndTimeMs < req.StartTimeMs || (req.VersionID == 0) == (strings.TrimSpace(req.InstrumentID) == "") {
		return resolvedSource{}, ErrInvalidSource
	}
	if req.VersionID != 0 {
		var version saasstore.MarketDataVersion
		if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND status=? AND integrity_status=?", req.VersionID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).First(&version).Error; err != nil {
			return resolvedSource{}, ErrUnsupportedSource
		}
		if version.Interval != req.Interval || version.ArtifactKind == marketversion.ArtifactKindLocalPerturbation || version.HasPerturbationAncestor {
			return resolvedSource{}, ErrUnsupportedSource
		}
		var rows []saasstore.MarketDataVersionBar
		if err := s.db.WithContext(ctx).Where("version_id=? AND open_time BETWEEN ? AND ?", version.ID, req.StartTimeMs, req.EndTimeMs).Order("ordinal ASC").Find(&rows).Error; err != nil {
			return resolvedSource{}, err
		}
		if len(rows) == 0 {
			return resolvedSource{}, ErrInvalidSource
		}
		var previousRow saasstore.MarketDataVersionBar
		var previous *float64
		if s.db.WithContext(ctx).Where("version_id=? AND open_time < ?", version.ID, rows[0].OpenTime).Order("ordinal DESC").First(&previousRow).Error == nil {
			value := previousRow.Close
			previous = &value
		}
		id := version.InstrumentID
		if version.OutputInstrumentID != nil {
			id = *version.OutputInstrumentID
		}
		desc := SourceDescriptor{InstrumentID: id, DataSource: version.DataSource, Symbol: version.Symbol, DisplayName: version.Symbol, Interval: version.Interval, VersionID: version.ID, ContentHash: version.ContentHash, ArtifactKind: version.ArtifactKind, Immutable: true}
		return resolvedSource{descriptor: desc, bars: versionRows(rows), previous: previous, market: version.Market, timezone: version.Timezone, calendarID: version.CalendarID, calendarVersion: version.CalendarVersion, calendarHash: version.CalendarHash, sourceVersionID: version.ID}, nil
	}
	var instrument saasstore.ResearchInstrument
	if err := s.db.WithContext(ctx).Where("id=? AND enabled=? AND internal_only=?", strings.ToUpper(strings.TrimSpace(req.InstrumentID)), true, false).First(&instrument).Error; err != nil || instrument.DataSource == marketdata.DataSourceFRED {
		return resolvedSource{}, ErrUnsupportedSource
	}
	if instrument.DataSource == marketdata.DataSourceGenerated {
		var metadata saasstore.DatasetMetadata
		if s.db.WithContext(ctx).Where("instrument_id=? AND data_source=? AND symbol=? AND interval=?", instrument.ID, instrument.DataSource, instrument.Symbol, req.Interval).First(&metadata).Error != nil || metadata.PriceAdjustment != marketdata.PriceAdjustmentGeneratedDailyLeverage {
			return resolvedSource{}, ErrUnsupportedSource
		}
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time BETWEEN ? AND ?", instrument.ID, instrument.DataSource, instrument.Symbol, req.Interval, req.StartTimeMs, req.EndTimeMs).Order("open_time ASC").Find(&rows).Error; err != nil {
		return resolvedSource{}, err
	}
	if len(rows) == 0 {
		return resolvedSource{}, ErrInvalidSource
	}
	var previousRow saasstore.KLine
	var previous *float64
	if s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time < ?", instrument.ID, instrument.DataSource, instrument.Symbol, req.Interval, rows[0].OpenTime).Order("open_time DESC").First(&previousRow).Error == nil {
		value := previousRow.Close
		previous = &value
	}
	desc := SourceDescriptor{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol, DisplayName: instrument.DisplayName, Interval: req.Interval, ArtifactKind: sourceArtifactKind(instrument.DataSource), Immutable: false}
	return resolvedSource{descriptor: desc, bars: klineRows(rows), previous: previous, market: instrument.Market, timezone: marketTimezone(instrument.Market), calendarID: fmt.Sprintf("actual-slots:%s:%s", instrument.ID, req.Interval), calendarVersion: marketversion.CalendarFromVersionVersion}, nil
}

func (s *Service) PlanGroup(ctx context.Context, userID uint, req GroupPlanRequest) (GroupPlan, error) {
	resolved, err := s.resolveSource(ctx, userID, req.Source)
	if err != nil {
		return GroupPlan{}, err
	}
	identity := core.SourceIdentity{InstrumentID: resolved.descriptor.InstrumentID, DataSource: resolved.descriptor.DataSource, Symbol: resolved.descriptor.Symbol, Interval: req.Source.Interval}
	hash, err := core.SourceContentHash(identity, resolved.bars[0].OpenTime, resolved.bars[len(resolved.bars)-1].OpenTime, resolved.previous, resolved.bars)
	if err != nil {
		return GroupPlan{}, err
	}
	wickWarning := true
	for _, bar := range resolved.bars {
		if bar.High > maxFloat(bar.Open, bar.Close) || bar.Low < minFloat(bar.Open, bar.Close) {
			wickWarning = false
			break
		}
	}
	plan := GroupPlan{SchemaVersion: core.SnapshotSchema, Source: resolved.descriptor, ActualStartTimeMs: resolved.bars[0].OpenTime, ActualEndTimeMs: resolved.bars[len(resolved.bars)-1].OpenTime, BarCount: len(resolved.bars), PreviousClosePresent: resolved.previous != nil, SourceContentHash: hash, EstimatedBytes: int64(len(resolved.bars)) * estimatedBytesPerBar, SourceVersion: sourceVersion(resolved), WickWarning: wickWarning}
	if resolved.previous != nil {
		plan.PreviousClose = *resolved.previous
	}
	plan.PlanHash, _, err = core.CanonicalHash(struct {
		SchemaVersion   string           `json:"schema_version"`
		Source          SourceDescriptor `json:"source"`
		Start           int64            `json:"start"`
		End             int64            `json:"end"`
		Count           int              `json:"count"`
		PreviousPresent bool             `json:"previous_present"`
		Previous        float64          `json:"previous"`
		SourceHash      string           `json:"source_hash"`
		SourceVersion   string           `json:"source_version"`
	}{plan.SchemaVersion, plan.Source, plan.ActualStartTimeMs, plan.ActualEndTimeMs, plan.BarCount, plan.PreviousClosePresent, plan.PreviousClose, plan.SourceContentHash, plan.SourceVersion})
	return plan, err
}

func (s *Service) CreateGroup(ctx context.Context, userID uint, req CreateGroupRequest) (GroupDescriptor, error) {
	plan, err := s.PlanGroup(ctx, userID, req.PlanRequest)
	if err != nil {
		return GroupDescriptor{}, err
	}
	if plan.PlanHash != strings.TrimSpace(req.PlanHash) {
		return GroupDescriptor{}, ErrStalePlan
	}
	resolved, err := s.resolveSource(ctx, userID, req.PlanRequest.Source)
	if err != nil {
		return GroupDescriptor{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "局部行情擾動"
	}
	tagsRaw, _ := compute.CanonicalJSON(cleanStrings(req.Tags))
	groupKeyRaw, _ := compute.CanonicalJSON(map[string]any{"plan_hash": plan.PlanHash, "name": name, "notes": strings.TrimSpace(req.Notes), "tags": json.RawMessage(tagsRaw)})
	groupKey := "perturbation-group:v1:" + compute.HashBytes(groupKeyRaw)
	var group saasstore.PerturbationGroup
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_user_id=? AND group_key=?", userID, groupKey).First(&group).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var snapshot saasstore.PerturbationSourceSnapshot
		if err := tx.Where("owner_user_id=? AND source_content_hash=?", userID, plan.SourceContentHash).First(&snapshot).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			created, createErr := s.createSnapshot(tx, userID, plan, resolved)
			if createErr != nil {
				return createErr
			}
			snapshot = created
		} else if err != nil {
			return err
		}
		series := saasstore.MarketSeries{OwnerUserID: userID, Name: name, Notes: strings.TrimSpace(req.Notes), Tags: tagsRaw}
		if err := tx.Create(&series).Error; err != nil {
			return err
		}
		group = saasstore.PerturbationGroup{OwnerUserID: userID, GroupKey: groupKey, Name: name, Notes: strings.TrimSpace(req.Notes), Tags: tagsRaw, SourceSnapshotID: snapshot.ID, MarketSeriesID: series.ID, AlgorithmVersion: core.AlgorithmVersion}
		return tx.Create(&group).Error
	})
	if err != nil {
		return GroupDescriptor{}, err
	}
	return s.GetGroup(ctx, userID, group.ID, true)
}

func (s *Service) createSnapshot(tx *gorm.DB, userID uint, plan GroupPlan, resolved resolvedSource) (saasstore.PerturbationSourceSnapshot, error) {
	instrumentID := internalID("LSP", plan.SourceContentHash)
	instrument := saasstore.ResearchInstrument{ID: instrumentID, Symbol: instrumentID, DisplayName: "擾動研究來源快照", DataSource: marketdata.DataSourceGenerated, SupportedIntervals: mustJSON([]string{plan.Source.Interval}), AvailableStartMs: mustJSON(map[string]int64{plan.Source.Interval: plan.ActualStartTimeMs}), Market: resolved.market, SortOrder: 900000, Enabled: true, InternalOnly: true}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&instrument).Error; err != nil {
		return saasstore.PerturbationSourceSnapshot{}, err
	}
	snapshotKey := fmt.Sprintf("%d|p13|%s", userID, plan.SourceContentHash)
	now := time.Now().UTC()
	outputID := instrumentID
	planRaw, _ := compute.CanonicalJSON(plan)
	direct, recursive, hasPerturbationAncestor, err := collectSnapshotLineage(tx, resolved.sourceVersionID)
	if err != nil {
		return saasstore.PerturbationSourceSnapshot{}, err
	}
	version := saasstore.MarketDataVersion{OwnerUserID: userID, SnapshotKey: &snapshotKey, SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion, ArtifactKind: marketversion.ArtifactKindSourceSnapshot, GeneratorVersion: "p13-source-snapshot-v1", PrecisionVersion: marketversion.PricePrecisionVersion, Status: marketversion.VersionStatusCompleted, IntegrityStatus: marketversion.IntegrityValid, ContentHash: plan.SourceContentHash, PlanHash: plan.PlanHash, Plan: planRaw, InstrumentID: instrumentID, DataSource: marketdata.DataSourceGenerated, Symbol: instrumentID, Market: resolved.market, Timezone: resolved.timezone, Interval: plan.Source.Interval, CalendarID: resolved.calendarID, CalendarVersion: resolved.calendarVersion, CalendarHash: firstNonempty(resolved.calendarHash, plan.SourceContentHash), BarCount: len(resolved.bars), StartTimeMs: plan.ActualStartTimeMs, EndTimeMs: plan.ActualEndTimeMs, PreviousClosePresent: plan.PreviousClosePresent, PreviousClose: plan.PreviousClose, HasPerturbationAncestor: hasPerturbationAncestor, InternalOnly: true, Published: true, OutputInstrumentID: &outputID, CompletedAt: &now}
	if err := tx.Create(&version).Error; err != nil {
		return saasstore.PerturbationSourceSnapshot{}, err
	}
	rows := make([]saasstore.MarketDataVersionBar, 0, len(resolved.bars))
	for index, bar := range resolved.bars {
		rows = append(rows, saasstore.MarketDataVersionBar{VersionID: version.ID, Ordinal: index, OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume})
	}
	if err := tx.CreateInBatches(&rows, 1000).Error; err != nil {
		return saasstore.PerturbationSourceSnapshot{}, err
	}
	if resolved.sourceVersionID != 0 {
		if err := tx.Create(&saasstore.MarketDataVersionSource{VersionID: version.ID, SourceVersionID: resolved.sourceVersionID, SourceOrder: 0, SourceRole: "snapshot_source", SourceHash: resolved.descriptor.ContentHash}).Error; err != nil {
			return saasstore.PerturbationSourceSnapshot{}, err
		}
	}
	snapshot := saasstore.PerturbationSourceSnapshot{OwnerUserID: userID, SourceContentHash: plan.SourceContentHash, SchemaVersion: core.SnapshotSchema, Status: marketversion.VersionStatusCompleted, SourceVersionID: version.ID, OriginalInstrumentID: resolved.descriptor.InstrumentID, OriginalDataSource: resolved.descriptor.DataSource, OriginalSymbol: resolved.descriptor.Symbol, Interval: plan.Source.Interval, StartTimeMs: plan.ActualStartTimeMs, EndTimeMs: plan.ActualEndTimeMs, PreviousClosePresent: plan.PreviousClosePresent, PreviousClose: plan.PreviousClose, BarCount: len(resolved.bars), DirectLineage: mustJSON(direct), RecursiveLineage: mustJSON(recursive), HasPerturbationAncestor: hasPerturbationAncestor, CompletedAt: &now}
	if err := tx.Create(&snapshot).Error; err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func collectSnapshotLineage(tx *gorm.DB, sourceVersionID uint) ([]map[string]any, []map[string]any, bool, error) {
	if sourceVersionID == 0 {
		return []map[string]any{}, []map[string]any{}, false, nil
	}
	visited := map[uint]bool{}
	recursive := []map[string]any{}
	hasPerturbationAncestor := false
	var walk func(uint, string, int) error
	walk = func(versionID uint, role string, order int) error {
		if visited[versionID] {
			return nil
		}
		visited[versionID] = true
		var version saasstore.MarketDataVersion
		if err := tx.Where("id=?", versionID).First(&version).Error; err != nil {
			return err
		}
		recursive = append(recursive, map[string]any{"version_id": version.ID, "content_hash": version.ContentHash, "artifact_kind": version.ArtifactKind, "source_role": role, "source_order": order})
		if version.ArtifactKind == marketversion.ArtifactKindLocalPerturbation || version.HasPerturbationAncestor {
			hasPerturbationAncestor = true
		}
		var edges []saasstore.MarketDataVersionSource
		if err := tx.Where("version_id=?", version.ID).Order("source_order ASC,id ASC").Find(&edges).Error; err != nil {
			return err
		}
		for _, edge := range edges {
			if err := walk(edge.SourceVersionID, edge.SourceRole, edge.SourceOrder); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(sourceVersionID, "snapshot_source", 0); err != nil {
		return nil, nil, false, err
	}
	direct := append([]map[string]any(nil), recursive[:1]...)
	return direct, recursive, hasPerturbationAncestor, nil
}

func (s *Service) ListGroups(ctx context.Context, userID uint, includeArchived bool) ([]GroupDescriptor, error) {
	var rows []saasstore.PerturbationGroup
	q := s.db.WithContext(ctx).Where("owner_user_id=?", userID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}
	if err := q.Order("updated_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]GroupDescriptor, 0, len(rows))
	for _, row := range rows {
		item, err := s.GetGroup(ctx, userID, row.ID, false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) GetGroup(ctx context.Context, userID, groupID uint, includeVariants bool) (GroupDescriptor, error) {
	var group saasstore.PerturbationGroup
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", groupID, userID).First(&group).Error; err != nil {
		return GroupDescriptor{}, ErrNotFound
	}
	var snapshot saasstore.PerturbationSourceSnapshot
	if err := s.db.WithContext(ctx).First(&snapshot, group.SourceSnapshotID).Error; err != nil {
		return GroupDescriptor{}, err
	}
	tags := []string{}
	_ = json.Unmarshal(group.Tags, &tags)
	out := GroupDescriptor{ID: group.ID, Name: group.Name, Notes: group.Notes, Tags: tags, AlgorithmVersion: group.AlgorithmVersion, Archived: group.ArchivedAt != nil, CreatedAt: group.CreatedAt.UTC().Format(time.RFC3339Nano), Snapshot: snapshotDescriptor(snapshot)}
	if includeVariants {
		variants, err := s.ListVariants(ctx, userID, group.ID, true)
		if err != nil {
			return GroupDescriptor{}, err
		}
		out.Variants = variants
	}
	return out, nil
}

func (s *Service) UpdateGroupMetadata(ctx context.Context, userID, groupID uint, req MetadataRequest) (GroupDescriptor, error) {
	tags, _ := compute.CanonicalJSON(cleanStrings(req.Tags))
	updates := map[string]any{"notes": strings.TrimSpace(req.Notes), "tags": tags}
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	r := s.db.WithContext(ctx).Model(&saasstore.PerturbationGroup{}).Where("id=? AND owner_user_id=?", groupID, userID).Updates(updates)
	if r.Error != nil {
		return GroupDescriptor{}, r.Error
	}
	if r.RowsAffected == 0 {
		return GroupDescriptor{}, ErrNotFound
	}
	return s.GetGroup(ctx, userID, groupID, true)
}

func (s *Service) ArchiveGroup(ctx context.Context, userID, groupID uint) error {
	now := time.Now().UTC()
	r := s.db.WithContext(ctx).Model(&saasstore.PerturbationGroup{}).Where("id=? AND owner_user_id=? AND archived_at IS NULL", groupID, userID).Update("archived_at", now)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func sourceArtifactKind(source string) string {
	if source == marketdata.DataSourceGenerated {
		return marketversion.ArtifactKindDailyLeverage
	}
	return marketversion.ArtifactKindSourceSnapshot
}
func sourceVersion(source resolvedSource) string {
	if source.sourceVersionID != 0 {
		return source.descriptor.ContentHash
	}
	hash, _, _ := core.CanonicalHash(source.bars)
	return hash
}
func versionRows(rows []saasstore.MarketDataVersionBar) []core.Bar {
	out := make([]core.Bar, 0, len(rows))
	for _, r := range rows {
		out = append(out, core.Bar{OpenTime: r.OpenTime, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: r.Volume})
	}
	return out
}
func klineRows(rows []saasstore.KLine) []core.Bar {
	out := make([]core.Bar, 0, len(rows))
	for _, r := range rows {
		out = append(out, core.Bar{OpenTime: r.OpenTime, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close, Volume: r.Volume})
	}
	return out
}
func marketTimezone(market string) string {
	switch strings.ToLower(market) {
	case "taiwan":
		return "Asia/Taipei"
	case "crypto":
		return "UTC"
	default:
		return "America/New_York"
	}
}
func internalID(prefix, hash string) string {
	clean := strings.ReplaceAll(hash, ":", "")
	if len(clean) > 29 {
		clean = clean[len(clean)-29:]
	}
	return strings.ToUpper(prefix + clean)
}
func mustJSON(v any) saasstore.JSONB { raw, _ := compute.CanonicalJSON(v); return raw }
func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func snapshotDescriptor(s saasstore.PerturbationSourceSnapshot) SnapshotDescriptor {
	return SnapshotDescriptor{ID: s.ID, SourceContentHash: s.SourceContentHash, SourceVersionID: s.SourceVersionID, OriginalInstrumentID: s.OriginalInstrumentID, OriginalDataSource: s.OriginalDataSource, OriginalSymbol: s.OriginalSymbol, Interval: s.Interval, StartTimeMs: s.StartTimeMs, EndTimeMs: s.EndTimeMs, BarCount: s.BarCount, Status: s.Status}
}
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func firstNonempty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func generateSeeds(count int) ([]string, error) {
	if count < 1 || count > 10000 {
		return nil, ErrInvalidSeed
	}
	out := make([]string, 0, count)
	seen := map[string]bool{}
	for len(out) < count {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		seed := fmt.Sprintf("%d", binary.BigEndian.Uint64(b[:]))
		if !seen[seed] {
			seen[seed] = true
			out = append(out, seed)
		}
	}
	return out, nil
}
