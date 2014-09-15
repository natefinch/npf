// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package stats

import (
	"time"

	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/charmstore"
)

// CheckCounterSum checks that statistics are properly collected.
// It retries a few times as they are generally collected in background.
func CheckCounterSum(c *gc.C, store *charmstore.Store, key []string, prefix bool, expected int64) {
	var sum int64
	for retry := 0; retry < 10; retry++ {
		time.Sleep(100 * time.Millisecond)
		req := charmstore.CounterRequest{
			Key:    key,
			Prefix: prefix,
		}
		cs, err := store.Counters(&req)
		c.Assert(err, gc.IsNil)
		if sum = cs[0].Count; sum == expected {
			if expected == 0 && retry < 2 {
				continue // Wait a bit to make sure.
			}
			return
		}
	}
	c.Errorf("counter sum for %#v is %d, want %d", key, sum, expected)
}
