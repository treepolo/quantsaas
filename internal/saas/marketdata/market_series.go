package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"quantsaas/internal/marketversion"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

func (s *Service) MarketSeries(ctx context.Context, userID uint, includeArchived bool) ([]MarketSeriesResult, error) {
	query := s.db.WithContext(ctx).Where("owner_user_id = ?", userID)
	if !includeArchived {
		query = query.Where("archived_at IS NULL")
	}
	var rows []saasstore.MarketSeries
	if err := query.Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]MarketSeriesResult, 0, len(rows))
	for _, row := range rows {
		var versions []saasstore.MarketDataVersion
		versionQuery := s.db.WithContext(ctx).Where("market_series_id = ? AND owner_user_id = ?", row.ID, userID)
		if !includeArchived {
			versionQuery = versionQuery.Where("archived_at IS NULL")
		}
		if err := versionQuery.Order("version_number DESC, id DESC").Find(&versions).Error; err != nil {
			return nil, err
		}
		item := MarketSeriesResult{ID: row.ID, Name: row.Name, Notes: row.Notes, Tags: []string{}, Archived: row.ArchivedAt != nil, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
		_ = json.Unmarshal(row.Tags, &item.Tags)
		item.Versions = make([]MarketVersionResult, 0, len(versions))
		for _, version := range versions {
			item.Versions = append(item.Versions, MarketVersionResult{
				ID: version.ID, VersionNumber: version.VersionNumber, ArtifactKind: version.ArtifactKind,
				ContentHash: version.ContentHash, PlanHash: version.PlanHash, InstrumentID: version.InstrumentID,
				Interval: version.Interval, BarCount: version.BarCount, StartTimeMs: version.StartTimeMs,
				EndTimeMs: version.EndTimeMs, Status: version.Status, IntegrityStatus: version.IntegrityStatus,
				Published: version.Published, Archived: version.ArchivedAt != nil,
				CreatedAt: version.CreatedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) ArchiveMarketVersion(ctx context.Context, userID, versionID uint) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&saasstore.MarketDataVersion{}).
		Where("id = ? AND owner_user_id = ? AND status = ? AND published = true AND archived_at IS NULL", versionID, userID, marketversion.VersionStatusCompleted).
		Update("archived_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrRecompositionPlanNotFound
	}
	return nil
}

func (s *Service) ArchiveMarketSeries(ctx context.Context, userID, seriesID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var series saasstore.MarketSeries
		if err := tx.Where("id = ? AND owner_user_id = ?", seriesID, userID).First(&series).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRecompositionPlanNotFound
			}
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&series).Update("archived_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.MarketDataVersion{}).
			Where("market_series_id = ? AND owner_user_id = ? AND status = ? AND published = true AND archived_at IS NULL", seriesID, userID, marketversion.VersionStatusCompleted).
			Update("archived_at", now).Error
	})
}
