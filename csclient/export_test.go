// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient

var (
	Hyphenate        = hyphenate
	UploadArchive    = (*Client).uploadArchive
	DefaultServerURL = &serverURL
)
