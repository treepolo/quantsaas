package performancereport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	performancecore "quantsaas/internal/performance"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

var (
	ErrAccessNotFound = errors.New("找不到報酬分析或來源回測")
	ErrInProgress     = errors.New("相同設定的報酬分析仍在計算")
)

type CreateRequest struct {
	RiskFreeAnnualRate      float64 `json:"risk_free_annual_rate"`
	HistogramBins           int     `json:"histogram_bins"`
	BetaBenchmarkInstrument string  `json:"beta_benchmark_instrument_id"`
}

type SourceResultDescriptor struct {
	ID             uint                        `json:"id"`
	Status         string                      `json:"status"`
	ResultVersion  string                      `json:"result_version"`
	ContentHash    string                      `json:"content_hash"`
	Spec           backtestresult.SpecSnapshot `json:"spec"`
	Summary        *backtestresult.SummaryData `json:"summary,omitempty"`
	BacktestRunIDs []uint                      `json:"backtest_run_ids"`
}

type Descriptor struct {
	ID                            uint                     `json:"id"`
	Status                        string                   `json:"status"`
	AnalysisKey                   string                   `json:"analysis_key"`
	AnalysisVersion               string                   `json:"analysis_version"`
	SchemaVersion                 string                   `json:"schema_version"`
	SettingsHash                  string                   `json:"settings_hash"`
	Settings                      ResolvedSettings         `json:"settings"`
	ContentHash                   string                   `json:"content_hash,omitempty"`
	BacktestResultID              uint                     `json:"backtest_result_id"`
	AnnualizationBacktestResultID uint                     `json:"annualization_backtest_result_id"`
	Summary                       *performancecore.Summary `json:"summary,omitempty"`
	ChartManifest                 *ChartManifest           `json:"chart_manifest,omitempty"`
	SourceResult                  SourceResultDescriptor   `json:"source_result"`
	CreatedAt                     string                   `json:"created_at"`
	CompletedAt                   string                   `json:"completed_at,omitempty"`
	Error                         string                   `json:"error,omitempty"`
	Reused                        bool                     `json:"reused"`
}

type ChartResponse struct {
	ReportID    uint            `json:"report_id"`
	Kind        string          `json:"kind"`
	ContentHash string          `json:"content_hash"`
	PointCount  int             `json:"point_count"`
	Data        json.RawMessage `json:"data"`
}

type GenomeSummaryResponse struct {
	GenomeID         uint        `json:"genome_id"`
	BacktestRunID    *uint       `json:"backtest_run_id,omitempty"`
	BacktestResultID *uint       `json:"backtest_result_id,omitempty"`
	Report           *Descriptor `json:"report,omitempty"`
}

type Service struct {
	db        *gorm.DB
	reports   *Store
	results   *backtestresult.Store
	backtests *backtest.Service
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, reports: NewStore(db), results: backtestresult.NewStore(db), backtests: backtest.NewService(db)}
}

func (s *Service) Create(ctx context.Context, userID uint, resultID uint, request CreateRequest) (*Descriptor, error) {
	if err := validateCreateRequest(request); err != nil {
		return nil, err
	}
	if err := s.authorizeResult(ctx, userID, resultID); err != nil {
		return nil, err
	}
	if _, err := s.results.VerifyResult(ctx, resultID); err != nil {
		return nil, err
	}
	source, err := s.results.Load(ctx, resultID, true)
	if err != nil {
		return nil, err
	}
	if source.Result.PathState != saasstore.BacktestPathStateAvailable || len(source.Path()) < 2 {
		return nil, fmt.Errorf("來源回測沒有可用的完整路徑")
	}
	annualization, err := s.backtests.EnsureNoCashFlowResult(ctx, userID, resultID)
	if err != nil {
		if errors.Is(err, backtest.ErrResultInProgress) {
			return nil, ErrInProgress
		}
		return nil, err
	}
	betaSeries, settings, err := s.resolveSettingsAndBenchmark(ctx, source, request)
	if err != nil {
		return nil, err
	}
	identity, err := BuildIdentity(IdentitySnapshot{
		BacktestResultID: source.Result.ID, BacktestResultVersion: source.Result.ResultVersion,
		BacktestResultContentHash:          source.Result.ContentHash,
		AnnualizationBacktestResultID:      annualization.Result.ID,
		AnnualizationBacktestResultVersion: annualization.Result.ResultVersion,
		AnnualizationResultContentHash:     annualization.Result.ContentHash,
		Settings:                           settings,
	})
	if err != nil {
		return nil, err
	}
	reservation, err := s.reports.Reserve(ctx, identity)
	if err != nil {
		return nil, err
	}
	if reservation.Reusable {
		loaded, err := s.reports.Load(ctx, reservation.Report.ID, false)
		if err != nil {
			return nil, err
		}
		descriptor, err := s.descriptor(ctx, userID, loaded)
		if descriptor != nil {
			descriptor.Reused = true
		}
		return descriptor, err
	}
	if !reservation.Created {
		return nil, ErrInProgress
	}
	if err := s.reports.MarkRunning(ctx, reservation.Report.ID); err != nil {
		return nil, err
	}
	analysis, err := performancecore.Analyze(
		performancePoints(source.Path()), performancePoints(annualization.Path()), betaSeries,
		performancecore.Config{RiskFreeAnnualRate: settings.RiskFreeAnnualRate, HistogramBins: settings.HistogramBins},
	)
	if err != nil {
		_ = s.reports.Fail(ctx, reservation.Report.ID, err)
		return nil, err
	}
	artifacts, err := BuildArtifacts(identity, analysis)
	if err != nil {
		_ = s.reports.Fail(ctx, reservation.Report.ID, err)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.reports.Cancel(context.Background(), reservation.Report.ID, err.Error())
		return nil, err
	}
	if err := s.reports.Complete(ctx, reservation.Report.ID, identity, artifacts); err != nil {
		_ = s.reports.Fail(ctx, reservation.Report.ID, err)
		return nil, err
	}
	if _, err := s.reports.Verify(ctx, reservation.Report.ID, true); err != nil {
		_ = s.reports.Invalidate(ctx, reservation.Report.ID, err.Error())
		return nil, err
	}
	loaded, err := s.reports.Load(ctx, reservation.Report.ID, false)
	if err != nil {
		return nil, err
	}
	return s.descriptor(ctx, userID, loaded)
}

func (s *Service) ListForResult(ctx context.Context, userID uint, resultID uint) ([]Descriptor, error) {
	if err := s.authorizeResult(ctx, userID, resultID); err != nil {
		return nil, err
	}
	models, err := s.reports.ListForResult(ctx, resultID)
	if err != nil {
		return nil, err
	}
	items := make([]Descriptor, 0, len(models))
	for _, model := range models {
		loaded, err := s.reports.Load(ctx, model.ID, false)
		if err != nil {
			return nil, err
		}
		descriptor, err := s.descriptor(ctx, userID, loaded)
		if err != nil {
			return nil, err
		}
		items = append(items, *descriptor)
	}
	return items, nil
}

func (s *Service) Get(ctx context.Context, userID uint, reportID uint) (*Descriptor, error) {
	loaded, err := s.reports.Load(ctx, reportID, false)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	if err := s.authorizeResult(ctx, userID, loaded.Report.BacktestResultID); err != nil {
		return nil, err
	}
	if _, err := s.reports.Verify(ctx, reportID, false); err != nil {
		return nil, err
	}
	return s.descriptor(ctx, userID, loaded)
}

func (s *Service) GetChart(ctx context.Context, userID uint, reportID uint, kind string) (*ChartResponse, error) {
	loaded, err := s.reports.Load(ctx, reportID, false)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	if err := s.authorizeResult(ctx, userID, loaded.Report.BacktestResultID); err != nil {
		return nil, err
	}
	payload, model, err := s.reports.LoadChart(ctx, reportID, strings.TrimSpace(kind))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	return &ChartResponse{ReportID: reportID, Kind: model.Kind, ContentHash: model.ContentHash, PointCount: model.PointCount, Data: payload}, nil
}

func (s *Service) Verify(ctx context.Context, userID uint, reportID uint) (IntegrityReport, error) {
	loaded, err := s.reports.Load(ctx, reportID, false)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return IntegrityReport{}, ErrAccessNotFound
		}
		return IntegrityReport{}, err
	}
	if err := s.authorizeResult(ctx, userID, loaded.Report.BacktestResultID); err != nil {
		return IntegrityReport{}, err
	}
	return s.reports.Verify(ctx, reportID, true)
}

func (s *Service) LatestForGenome(ctx context.Context, userID uint, genomeID uint) (*GenomeSummaryResponse, error) {
	response := &GenomeSummaryResponse{GenomeID: genomeID}
	id := strconv.FormatUint(uint64(genomeID), 10)
	var run saasstore.BacktestRun
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND source = ? AND backtest_result_id IS NOT NULL AND status = ?", userID, backtest.SourceCandidate, saasstore.BacktestStatusCompleted).
		Where("request ->> 'candidate_id' = ? OR request ->> 'genome_id' = ?", id, id).
		Order("created_at DESC, id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return response, nil
	}
	if err != nil {
		return nil, err
	}
	response.BacktestRunID = &run.ID
	response.BacktestResultID = run.BacktestResultID
	reports, err := s.reports.ListForResult(ctx, *run.BacktestResultID)
	if err != nil {
		return nil, err
	}
	for _, report := range reports {
		if report.Status != saasstore.PerformanceReportStatusCompleted && report.Status != saasstore.PerformanceReportStatusArchived && report.Status != saasstore.PerformanceReportStatusInvalidated {
			continue
		}
		loaded, err := s.reports.Load(ctx, report.ID, false)
		if err != nil {
			return nil, err
		}
		descriptor, err := s.descriptor(ctx, userID, loaded)
		if err != nil {
			return nil, err
		}
		response.Report = descriptor
		break
	}
	return response, nil
}

func validateCreateRequest(request CreateRequest) error {
	if math.IsNaN(request.RiskFreeAnnualRate) || math.IsInf(request.RiskFreeAnnualRate, 0) || request.RiskFreeAnnualRate <= -1 {
		return fmt.Errorf("無風險年利率必須大於 -100%% 且為有限數值")
	}
	if request.HistogramBins < 0 || request.HistogramBins > performancecore.MaximumHistogramBins {
		return fmt.Errorf("直方圖分箱數必須介於 1 到 %d", performancecore.MaximumHistogramBins)
	}
	return nil
}

func (s *Service) resolveSettingsAndBenchmark(ctx context.Context, source backtestresult.LoadedResult, request CreateRequest) ([]performancecore.SeriesPoint, ResolvedSettings, error) {
	settings := normalizeSettings(ResolvedSettings{RiskFreeAnnualRate: request.RiskFreeAnnualRate, HistogramBins: request.HistogramBins})
	instrumentID := strings.TrimSpace(request.BetaBenchmarkInstrument)
	if instrumentID == "" {
		return nil, settings, nil
	}
	var instrument saasstore.ResearchInstrument
	if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ?", instrumentID, true).First(&instrument).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, settings, fmt.Errorf("找不到可用的 Beta 基準標的")
		}
		return nil, settings, err
	}
	var supportedIntervals []string
	if err := json.Unmarshal(instrument.SupportedIntervals, &supportedIntervals); err != nil {
		return nil, settings, fmt.Errorf("Beta 基準標的的週期設定無法解析")
	}
	if !containsInterval(supportedIntervals, "1d") {
		return nil, settings, fmt.Errorf("Beta 基準需要日 K 資料")
	}
	identity, err := backtestresult.DecodeIdentity(source.Spec.Snapshot)
	if err != nil {
		return nil, settings, err
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?",
			instrument.ID, instrument.DataSource, instrument.Symbol, "1d", identity.Snapshot.StartTimeMs, identity.Snapshot.EndTimeMs).
		Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, settings, err
	}
	if len(rows) < 3 {
		return nil, settings, fmt.Errorf("Beta 基準在回測區間內至少需要三筆日 K")
	}
	bars := make([]quant.Bar, 0, len(rows))
	series := make([]performancecore.SeriesPoint, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
		series = append(series, performancecore.SeriesPoint{TimeMs: row.OpenTime, Value: row.Close})
	}
	datasetHash, err := backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
	if err != nil {
		return nil, settings, err
	}
	settings.BetaBenchmark = &BetaBenchmarkSettings{
		InstrumentID: instrument.ID, Symbol: instrument.Symbol, DataSource: instrument.DataSource,
		Interval: "1d", StartTimeMs: rows[0].OpenTime, EndTimeMs: rows[len(rows)-1].OpenTime,
		DatasetVersion: backtestresult.DatasetSchemaVersion, DatasetHash: datasetHash,
	}
	return series, settings, nil
}

func performancePoints(path []backtestresult.PathPoint) []performancecore.Point {
	points := make([]performancecore.Point, 0, len(path))
	for _, item := range path {
		benchmark := 0.0
		if item.BenchmarkEquity != nil {
			benchmark = *item.BenchmarkEquity
		}
		points = append(points, performancecore.Point{TimeMs: item.TimeMs, NAV: item.TotalEquity, BenchmarkNAV: benchmark, ActualExposure: item.ActualExposureWeight})
	}
	return points
}

func (s *Service) authorizeResult(ctx context.Context, userID uint, resultID uint) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("user_id = ? AND backtest_result_id = ?", userID, resultID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		if err := s.db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).
			Joins("JOIN kline_inverse_studies ON kline_inverse_studies.id = kline_inverse_evaluations.study_id").
			Where("kline_inverse_studies.owner_user_id = ? AND kline_inverse_evaluations.backtest_result_id = ? AND kline_inverse_evaluations.permanent = ?", userID, resultID, true).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrAccessNotFound
		}
	}
	return nil
}

func (s *Service) descriptor(ctx context.Context, userID uint, loaded LoadedReport) (*Descriptor, error) {
	source, err := s.results.Load(ctx, loaded.Report.BacktestResultID, false)
	if err != nil {
		return nil, err
	}
	identity, err := backtestresult.DecodeIdentity(source.Spec.Snapshot)
	if err != nil {
		return nil, err
	}
	var runIDs []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("user_id = ? AND backtest_result_id = ?", userID, source.Result.ID).
		Order("id ASC").Pluck("id", &runIDs).Error; err != nil {
		return nil, err
	}
	descriptor := &Descriptor{
		ID: loaded.Report.ID, Status: loaded.Report.Status, AnalysisKey: loaded.Report.AnalysisKey,
		AnalysisVersion: loaded.Report.AnalysisVersion, SchemaVersion: loaded.Report.SchemaVersion,
		SettingsHash: loaded.Report.SettingsHash, Settings: loaded.Settings, ContentHash: loaded.Report.ContentHash,
		BacktestResultID:              loaded.Report.BacktestResultID,
		AnnualizationBacktestResultID: loaded.Report.AnnualizationBacktestResultID,
		Summary:                       loaded.Summary, ChartManifest: loaded.Manifest,
		SourceResult: SourceResultDescriptor{
			ID: source.Result.ID, Status: source.Result.Status, ResultVersion: source.Result.ResultVersion,
			ContentHash: source.Result.ContentHash, Spec: identity.Snapshot, Summary: source.Summary, BacktestRunIDs: runIDs,
		},
		CreatedAt: loaded.Report.CreatedAt.Format(time.RFC3339), Error: loaded.Report.ErrorMessage,
	}
	if loaded.Report.CompletedAt != nil {
		descriptor.CompletedAt = loaded.Report.CompletedAt.Format(time.RFC3339)
	}
	return descriptor, nil
}

func containsInterval(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
