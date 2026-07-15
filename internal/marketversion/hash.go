package marketversion

import "quantsaas/internal/compute"

type SourceSnapshot struct {
	SchemaVersion        string  `json:"schema_version"`
	InstrumentID         string  `json:"instrument_id"`
	DataSource           string  `json:"data_source"`
	Symbol               string  `json:"symbol"`
	Market               string  `json:"market"`
	Timezone             string  `json:"timezone"`
	Interval             string  `json:"interval"`
	ArtifactKind         string  `json:"artifact_kind"`
	CalendarID           string  `json:"calendar_id"`
	CalendarVersion      string  `json:"calendar_version"`
	PreviousClosePresent bool    `json:"previous_close_present"`
	PreviousClose        float64 `json:"previous_close,omitempty"`
	Bars                 []Bar   `json:"-"`
}

func HashSourceSnapshot(snapshot SourceSnapshot) (string, error) {
	if snapshot.SchemaVersion == "" {
		snapshot.SchemaVersion = VersionSchemaVersion
	}
	raw, err := compute.CanonicalJSON(struct {
		SchemaVersion        string         `json:"schema_version"`
		InstrumentID         string         `json:"instrument_id"`
		DataSource           string         `json:"data_source"`
		Symbol               string         `json:"symbol"`
		Market               string         `json:"market"`
		Timezone             string         `json:"timezone"`
		Interval             string         `json:"interval"`
		ArtifactKind         string         `json:"artifact_kind"`
		CalendarID           string         `json:"calendar_id"`
		CalendarVersion      string         `json:"calendar_version"`
		PreviousClosePresent bool           `json:"previous_close_present"`
		PreviousClose        string         `json:"previous_close,omitempty"`
		Bars                 []canonicalBar `json:"bars"`
	}{
		SchemaVersion: snapshot.SchemaVersion, InstrumentID: snapshot.InstrumentID, DataSource: snapshot.DataSource,
		Symbol: snapshot.Symbol, Market: snapshot.Market, Timezone: snapshot.Timezone, Interval: snapshot.Interval,
		ArtifactKind: snapshot.ArtifactKind, CalendarID: snapshot.CalendarID, CalendarVersion: snapshot.CalendarVersion,
		PreviousClosePresent: snapshot.PreviousClosePresent, PreviousClose: decimal10(snapshot.PreviousClose),
		Bars: canonicalBars(snapshot.Bars),
	})
	if err != nil {
		return "", err
	}
	return "market-source:v1:" + compute.HashBytes(raw), nil
}

func HashCalendarSlots(version VersionIdentity, slots []int64) (string, error) {
	raw, err := compute.CanonicalJSON(struct {
		SchemaVersion string          `json:"schema_version"`
		Version       VersionIdentity `json:"version"`
		Slots         []int64         `json:"slots"`
	}{CalendarFromVersionVersion, version, slots})
	if err != nil {
		return "", err
	}
	return "market-calendar:v1:" + compute.HashBytes(raw), nil
}
