// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package params

// Define the kinds to be included in stats keys.
const (
	StatsArchiveDownload     = "archive-download"
	StatsArchiveDelete       = "archive-delete"
	StatsArchiveFailedUpload = "archive-failed-upload"
	StatsArchiveUpload       = "archive-upload"
	// The following kinds are in use in the legacy API.
	StatsCharmInfo    = "charm-info"
	StatsCharmMissing = "charm-missing"
)

// Statistic holds one element of a stats/counter
// response. See http://tinyurl.com/nkdovcf
type Statistic struct {
	Key   string `json:",omitempty"`
	Date  string `json:",omitempty"`
	Count int64
}

// StatsResponse holds the result of an
// id/meta/stats GET request. See http://tinyurl.com/lvyp2l5
type StatsResponse struct {
	// ArchiveDownloadCount is the global downloads count for a specific
	// revision of the entity.
	ArchiveDownloadCount int64
	// ArchiveDownloadCountLastDay is the download count in the last 24 hours
	// for a specific revision of the entity.
	ArchiveDownloadCountLastDay int64
	// ArchiveDownloadCountLastWeek is the download count in the last week for
	// a specific revision of the entity.
	ArchiveDownloadCountLastWeek int64
	// ArchiveDownloadCountLastMonth is the download count in the last month
	// for a specific revision of the entity.
	ArchiveDownloadCountLastMonth int64
	// ArchiveDownloadCountAllRevisions is the global downloads count for all
	// revisions of the entity.
	ArchiveDownloadCountAllRevisions int64
	// ArchiveDownloadCountLastDayAllRevisions is the download count in the
	// last 24 hours for all revisions of the entity.
	ArchiveDownloadCountLastDayAllRevisions int64
	// ArchiveDownloadCountLastWeekAllRevisions is the download count in the
	// last week for all revisions of the entity.
	ArchiveDownloadCountLastWeekAllRevisions int64
	// ArchiveDownloadCountLastMonthAllRevisions is the download count in the
	// last month for all revisions of the entity.
	ArchiveDownloadCountLastMonthAllRevisions int64
}
