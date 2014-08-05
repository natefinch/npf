// Copyright 2012 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"
	"strings"
	"time"

	"github.com/juju/errgo"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/params"
)

// GET stats/counter/key[:key]...?[by=unit]&start=date][&stop=date][&list=1]
// http://tinyurl.com/nkdovcf
func (s *handler) serveStatsCounter(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	base := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Index(base, "/") > 0 {
		return nil, params.ErrNotFound
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
		return nil, errgo.WithCausef(nil, params.ErrBadRequest, "invalid 'by' value: %q", v)
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
			return nil, errgo.WithCausef(err, params.ErrBadRequest, "invalid 'start' value %q", v)
		}
	}
	if v := r.Form.Get("stop"); v != "" {
		var err error
		req.Stop, err = time.Parse("2006-01-02", v)
		if err != nil {
			return nil, errgo.WithCausef(err, params.ErrBadRequest, "invalid 'stop' value %q", v)
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
