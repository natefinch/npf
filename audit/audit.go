// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package audit

import (
	"gopkg.in/juju/charm.v6-unstable"
	"time"
)

// Operation represents the type of an entry.
type Operation string

const (
	// OpSetPerm represents the setting of ACLs on an entity.
	// Required fields: Entity, ACL
	OpSetPerm Operation = "set-perm"

	// OpSetPromulgated represents the promulgation on an entity.
	// Required fields: Entity, Promulgated
	OpSetPromulgated Operation = "set-promulgated"
)

// ACL represents an access control list.
type ACL struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

// Entry represents an audit log entry.
type Entry struct {
	Time        time.Time        `json:"time"`
	User        string           `json:"user"`
	Op          Operation        `json:"op"`
	Entity      *charm.Reference `json:"entity,omitempty"`
	ACL         *ACL             `json:"acl,omitempty"`
	Promulgated bool             `json:"promulgation,omitempty"`
}
