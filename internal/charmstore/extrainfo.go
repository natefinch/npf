// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"encoding/json"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
)

func bundleExtraInfo(data *charm.BundleData) (map[string][]byte, error) {
	// Calculate the number of units and machines in the bundle.
	numUnits := 0
	numMachines := len(data.Machines)
	for _, service := range data.Services {
		// Collect the number of units.
		numUnits += service.NumUnits

		// Collect the number of machines.
		numPlacements := len(service.To)
		// Check if placement info is provided: if not, add a new machine for
		// each unit in the service.
		if numPlacements == 0 {
			numMachines += service.NumUnits
			continue
		}
		// Check for "new" placements, which means a new machine must be added.
		for _, location := range service.To {
			if location == "new" {
				numMachines++
			}
		}
		// If there are less elements in To than NumUnits, the last placement
		// element is replicated. For this reason, if the last element is
		// "new", we need to add more machines.
		if service.To[numPlacements-1] == "new" {
			numMachines += (service.NumUnits - numPlacements)
		}
	}

	// Convert obtained data to JSON.
	machinesCount, err := json.Marshal(numMachines)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	unitsCount, err := json.Marshal(numUnits)
	if err != nil {
		return nil, errgo.Mask(err)
	}

	return map[string][]byte{
		"machines-count": machinesCount,
		"units-count":    unitsCount,
	}, nil
}
