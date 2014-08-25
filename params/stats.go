// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package params

// Define the kinds to be included in stats keys.
// TODO frankban 2014-08-22:
//  increase the counters for the kinds below:
//  the only hooked kind is archive-download for now.
const (
	StatsArchiveDownload     = "archive-download"
	StatsArchiveDelete       = "archive-delete"
	StatsArchiveFailedUpload = "archive-failed-upload"
	StatsArchiveUpload       = "archive-upload"
	StatsInfo                = "info"
	StatsMissing             = "missing"
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
	// ArchiveDownloadCount is the downloads count for the entity.
	ArchiveDownloadCount int64
}
