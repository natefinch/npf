// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"encoding/json"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
)

func bundleExtraInfo(data *charm.BundleData) (map[string][]byte, error) {
	// Calculate the number of units in the bundle.
	numUnits := 0
	for _, service := range data.Services {
		numUnits += service.NumUnits
	}
	unitsCount, err := json.Marshal(numUnits)
	if err != nil {
		return nil, errgo.Mask(err)
	}

	return map[string][]byte{
		// TODO frankban 2014-10-02: add machines count.
		"units-count": unitsCount,
	}, nil
}
