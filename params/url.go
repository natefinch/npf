// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package params

import (
	"encoding/json"

	"gopkg.in/juju/charm.v2"
	"labix.org/v2/mgo/bson"
)

// CharmURL represents a charm or bundle URL. In the charm case, the URL may
// not include the series part.
// TODO(frankban): this type can be removed after changing charm.URL to be
// more tolerant when de-serializing series-less URLs.
type CharmURL charm.URL

func (u *CharmURL) MarshalJSON() ([]byte, error) {
	url := (*charm.URL)(u)
	return url.MarshalJSON()
}

func (u *CharmURL) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	url, err := ParseURL(s)
	if err != nil {
		return err
	}
	*u = *url
	return nil
}

// GetBSON turns u into a bson.Getter so it can be saved directly
// on a MongoDB database with mgo.
func (u *CharmURL) GetBSON() (interface{}, error) {
	url := (*charm.URL)(u)
	return url.GetBSON()
}

// SetBSON turns u into a bson.Setter so it can be loaded directly
// from a MongoDB database with mgo.
func (u *CharmURL) SetBSON(raw bson.Raw) error {
	if raw.Kind == 10 {
		return bson.SetZero
	}
	var s string
	err := raw.Unmarshal(&s)
	if err != nil {
		return err
	}
	url, err := ParseURL(s)
	if err != nil {
		return err
	}
	*u = *url
	return nil
}

// ParseURL turns a charm or bundle URL string into a CharmURL.
func ParseURL(urlStr string) (*CharmURL, error) {
	ref, series, err := charm.ParseReference(urlStr)
	if err != nil {
		return nil, err
	}
	return &CharmURL{
		Reference: ref,
		Series:    series,
	}, nil
}
