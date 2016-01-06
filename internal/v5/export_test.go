// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

var (
	ProcessIcon          = processIcon
	ErrProbablyNotXML    = errProbablyNotXML
	TestAddAuditCallback = &testAddAuditCallback

	BundleCharms              = (*ReqHandler).bundleCharms
	GetNewPromulgatedRevision = (*ReqHandler).getNewPromulgatedRevision

	ResolveURL = resolveURL
)
