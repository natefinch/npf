// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

var (
	ParseSearchParams         = parseSearchParams
	DefaultIcon               = defaultIcon
	ArchiveCachePublicMaxAge  = &archiveCachePublicMaxAge
	ParamsLogLevels           = paramsLogLevels
	ParamsLogTypes            = paramsLogTypes
	ProcessIcon               = processIcon
	ErrProbablyNotXML         = errProbablyNotXML
	UsernameAttr              = usernameAttr
	DelegatableMacaroonExpiry = delegatableMacaroonExpiry
	TestAddAuditCallback      = &testAddAuditCallback

	BundleCharms              = (*ReqHandler).bundleCharms
	GetNewPromulgatedRevision = (*ReqHandler).getNewPromulgatedRevision
	GroupsForUser             = (*ReqHandler).groupsForUser
	ServeArchive              = (*ReqHandler).serveArchive
)
