// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

var (
	BundleCharms                   = (*Handler).bundleCharms
	ParseSearchParams              = parseSearchParams
	DefaultIcon                    = defaultIcon
	ArchiveCacheVersionedMaxAge    = &archiveCacheVersionedMaxAge
	ArchiveCacheNonVersionedMaxAge = &archiveCacheNonVersionedMaxAge
	ParamsLogLevels                = paramsLogLevels
	ParamsLogTypes                 = paramsLogTypes
	ProcessIcon                    = processIcon
	ErrProbablyNotXML              = errProbablyNotXML
	UsernameAttr                   = usernameAttr
	GetNewPromulgatedRevision      = (*Handler).getNewPromulgatedRevision
	DelegatableMacaroonExpiry      = delegatableMacaroonExpiry
	GroupsForUser                  = (*Handler).groupsForUser
)
