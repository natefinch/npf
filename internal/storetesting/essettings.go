// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"encoding/json"
	"os"
	"path/filepath"
)

var esIndex map[string]interface{}
var esMapping map[string]interface{}

func init() {
	path := filepath.Join(os.Getenv("GOPATH"), "src", "github.com", "juju", "charmstore", "internal", "elasticsearch", "definitions")
	f, err := os.Open(filepath.Join(path, "index.json"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	if err := d.Decode(&esIndex); err != nil {
		panic(err)
	}
	f, err = os.Open(filepath.Join(path, "entity.json"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	d = json.NewDecoder(f)
	if err := d.Decode(&esMapping); err != nil {
		panic(err)
	}
}
