// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package series holds information about series supported in the
// charmstore.
package series // import "gopkg.in/juju/charmstore.v5-unstable/internal/series"

// Distribution represents a distribution supported by the charmstore.
// Every series will belong to a distribution.
type Distribution string

const (
	Ubuntu  Distribution = "ubuntu"
	CentOS  Distribution = "centos"
	Windows Distribution = "windows"
)

// SeriesInfo contains the information the charmstore knows about a
// series name.
type SeriesInfo struct {
	// CharmSeries holds whether this series name is for charms.
	CharmSeries bool

	// Distribution holds the Distribution this series belongs to.
	Distribution Distribution

	// SearchIndex holds wether charms in this series should be added
	// to the search index.
	SearchIndex bool

	// SearchBoost contains the relative boost given to charms in
	// this series when searching.
	SearchBoost float64
}

// Series contains the data charmstore knows about series names
var Series = map[string]SeriesInfo{
	// Bundle
	"bundle": SeriesInfo{false, "", true, 1.1255},

	// Ubuntu
	"oneiric": SeriesInfo{true, Ubuntu, false, 0},
	"precise": SeriesInfo{true, Ubuntu, true, 1.1125},
	"quantal": SeriesInfo{true, Ubuntu, false, 0},
	"raring":  SeriesInfo{true, Ubuntu, false, 0},
	"saucy":   SeriesInfo{true, Ubuntu, false, 0},
	"trusty":  SeriesInfo{true, Ubuntu, true, 1.125},
	"utopic":  SeriesInfo{true, Ubuntu, false, 0},
	"vivid":   SeriesInfo{true, Ubuntu, true, 1.101},
	"wily":    SeriesInfo{true, Ubuntu, true, 1.102},

	// Windows
	"win2012hvr2": SeriesInfo{true, Windows, true, 1.1},
	"win2012hv":   SeriesInfo{true, Windows, true, 1.1},
	"win2012r2":   SeriesInfo{true, Windows, true, 1.1},
	"win2012":     SeriesInfo{true, Windows, true, 1.1},
	"win7":        SeriesInfo{true, Windows, true, 1.1},
	"win8":        SeriesInfo{true, Windows, true, 1.1},
	"win81":       SeriesInfo{true, Windows, true, 1.1},
	"win10":       SeriesInfo{true, Windows, true, 1.1},
	"win2016":     SeriesInfo{true, Windows, true, 1.1},
	"win2016nano": SeriesInfo{true, Windows, true, 1.1},

	// Centos
	"centos7": SeriesInfo{true, CentOS, true, 1.1},
}
