// Copyright 2012 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/params"
)

// entityStatsKey returns a stats key for the given charm or bundle
// reference and the given kind.
// Entities' stats can then be retrieved like the following:
//   - kind:utopic:* -> all charms of a specific series;
//   - kind:trusty:django:* -> all revisions of a specific charm;
//   - kind:trusty:django:42 -> a specific promulgated charm;
//   - kind:trusty:django:42:* -> all user owned variations of a charm;
//   - kind:trusty:django:42:who -> a specific user charm.
// The above also applies to bundles (where the series is "bundle").
func entityStatsKey(url *charm.Reference, kind string) []string {
	key := []string{kind, url.Series, url.Name, strconv.Itoa(url.Revision)}
	if url.User != "" {
		key = append(key, url.User)
	}
	return key
}

// GET stats/counter/key[:key]...?[by=unit]&start=date][&stop=date][&list=1]
// http://tinyurl.com/nkdovcf
func (s *handler) serveStatsCounter(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	base := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Index(base, "/") > 0 {
		return nil, errgo.WithCausef(nil, params.ErrNotFound, "invalid key")
	}
	if base == "" {
		return nil, params.ErrForbidden
	}
	var by charmstore.CounterRequestBy
	switch v := r.Form.Get("by"); v {
	case "":
		by = charmstore.ByAll
	case "day":
		by = charmstore.ByDay
	case "week":
		by = charmstore.ByWeek
	default:
		return nil, badRequestf(nil, "invalid 'by' value %q", v)
	}
	req := charmstore.CounterRequest{
		Key:  strings.Split(base, ":"),
		List: r.Form.Get("list") == "1",
		By:   by,
	}
	if v := r.Form.Get("start"); v != "" {
		var err error
		req.Start, err = time.Parse("2006-01-02", v)
		if err != nil {
			return nil, badRequestf(err, "invalid 'start' value %q", v)
		}
	}
	if v := r.Form.Get("stop"); v != "" {
		var err error
		req.Stop, err = time.Parse("2006-01-02", v)
		if err != nil {
			return nil, badRequestf(err, "invalid 'stop' value %q", v)
		}
		// Cover all timestamps within the stop day.
		req.Stop = req.Stop.Add(24*time.Hour - 1*time.Second)
	}
	if req.Key[len(req.Key)-1] == "*" {
		req.Prefix = true
		req.Key = req.Key[:len(req.Key)-1]
		if len(req.Key) == 0 {
			return nil, errgo.WithCausef(nil, params.ErrForbidden, "unknown key")
		}
	}
	entries, err := s.store.Counters(&req)
	if err != nil {
		return nil, errgo.Notef(err, "cannot query counters")
	}

	var buf []byte
	var items []params.Statistic
	for i := range entries {
		entry := &entries[i]
		buf = buf[:0]
		if req.List {
			for j := range entry.Key {
				buf = append(buf, entry.Key[j]...)
				buf = append(buf, ':')
			}
			if entry.Prefix {
				buf = append(buf, '*')
			} else {
				buf = buf[:len(buf)-1]
			}
		}
		stat := params.Statistic{
			Key:   string(buf),
			Count: entry.Count,
		}
		if !entry.Time.IsZero() {
			stat.Date = entry.Time.Format("2006-01-02")
		}
		items = append(items, stat)
	}

	return items, nil
}
