// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

var (
	BundleCharms                   = (*Handler).bundleCharms
	ParseSearchParams              = parseSearchParams
	StartTime                      = &startTime
	DefaultIcon                    = defaultIcon
	ArchiveCacheVersionedMaxAge    = &archiveCacheVersionedMaxAge
	ArchiveCacheNonVersionedMaxAge = &archiveCacheNonVersionedMaxAge
)
