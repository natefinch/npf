// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package lppublish

import (
	"crypto/sha512"
	"fmt"
	"io"
	stdUrl "net/url"
	"os"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/params"
)

// publishLegacyCharm publishes all existing revisions of the given urls
// in the charm store, fetching them from the legacy charm
// store.
func (cl *charmLoader) publishLegacyCharm(urls []*charm.Reference) error {
	// An Info call on the first URL *should* tell us the latest revision of
	// all the URLs because in legacy charm store, they all match,
	// but we'll fetch info for all the URLs just to sanity check.
	infos, err := charm.Store.Info(urls[0])
	if err != nil {
		return errgo.Notef(err, "cannot get information on %q", urls)
	}
	if len(infos) != 1 {
		return errgo.Newf("unexpected response count %d, expected 1", len(infos))
	}
	alreadyUploaded, err := cl.allRevisions(urls)
	if err != nil {
		return errgo.Mask(err)
	}
	for rev := 0; rev <= infos[0].Revision; rev++ {
		for _, url := range urls {
			url.Revision = rev
		}
		if err := cl.putLegacyCharm(urls, alreadyUploaded); err != nil {
			if errgo.Cause(err) == params.ErrUnauthorized {
				return err
			}
			logger.Errorf("cannot put legacy charm: %v", err)
		}
	}
	return nil
}

// allRevisions returns all the revisions stored in the charm store for
// all of the given charm URLs. The returned map holds an entry
// for each revision of each the given urls, keyed by id string.
func (cl *charmLoader) allRevisions(urls []*charm.Reference) (map[string]bool, error) {
	ids := make([]string, len(urls))
	for i, url := range urls {
		ids[i] = "id=" + stdUrl.QueryEscape(url.String())
	}
	path := "meta/revision-info?" + strings.Join(ids, "&")

	var resp map[string]params.RevisionInfoResponse
	if err := cl.charmStoreGet(path, &resp); err != nil {
		if errgo.Cause(err) != params.ErrNotFound {
			return nil, errgo.Notef(err, "cannot retrieve revisions")
		}
		return nil, nil
	}
	have := make(map[string]bool)
	for _, revInfo := range resp {
		for _, url := range revInfo.Revisions {
			have[url.String()] = true
		}
	}
	return have, nil
}

func (cl *charmLoader) putLegacyCharm(urls []*charm.Reference, alreadyUploaded map[string]bool) error {
	var need []*charm.Reference
	for _, url := range urls {
		if !alreadyUploaded[url.String()] {
			need = append(need, url)
		}
	}
	if len(need) == 0 {
		logger.Infof("already uploaded: %s", urls)
		return nil
	}

	// All the promulgated versions of a given charm
	// will have the same content, so we just get a single
	// archive and digest and publish to all URLs.

	// Acquire digest.
	infos, err := charm.Store.Info(urls[0])
	if err != nil {
		return errgo.Notef(err, "cannot get info on %q", urls[0])
	}
	digest := infos[0].Digest

	// Acquire charm archive.
	ch, err := legacyCharmStoreGet(need[0])
	if err != nil {
		return errgo.Notef(err, "cannot get %q", need[0])
	}
	f, err := os.Open(ch.Path)
	if err != nil {
		return errgo.Mask(err)
	}
	defer f.Close()
	hasher := sha512.New384()
	size, err := io.Copy(hasher, f)
	if err != nil {
		return errgo.Notef(err, "cannot read charm archive: %v", err)
	}

	// Upload content to all URLs.
	hash := fmt.Sprintf("%x", hasher.Sum(nil))
	for _, url := range need {
		_, err := f.Seek(0, 0)
		if err != nil {
			return errgo.Mask(err)
		}
		_, err = cl.uploadArchive("PUT", f, url, size, hash)
		if err != nil {
			return errgo.NoteMask(err, fmt.Sprintf("cannot put %q", url), errgo.Is(params.ErrUnauthorized))
		}
		if err := cl.putDigest(url, digest); err != nil {
			return errgo.Mask(err)
		}
	}
	return nil
}

func legacyCharmStoreGet(url *charm.Reference) (*charm.CharmArchive, error) {
	url1, err := url.URL("")
	if err != nil {
		// We added the series earlier.
		panic(fmt.Errorf("cannot happen: %v", err))
	}
	ch, err := charm.Store.Get(url1)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return ch.(*charm.CharmArchive), nil
}
