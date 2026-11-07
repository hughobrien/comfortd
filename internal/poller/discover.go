// SPDX-License-Identifier: GPL-3.0-or-later

// Package poller discovers units and keeps the store fresh.
package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/hughobrien/comfortd/internal/fsutil"
	"github.com/hughobrien/comfortd/internal/store"
	"github.com/hughobrien/comfortd/pkg/kumo"
)

type API interface {
	Get(ctx context.Context, path string) (json.RawMessage, error)
}

// Discover lists units from the cloud and caches them; if the cloud fails it
// falls back to the cache.
func Discover(ctx context.Context, api API, cachePath string) ([]store.Unit, error) {
	units, err := discoverLive(ctx, api)
	if err == nil {
		if b, merr := json.Marshal(units); merr == nil {
			if werr := fsutil.WriteFileAtomic(cachePath, b); werr != nil {
				slog.Warn("could not write discovery cache", "err", werr)
			}
		}
		return units, nil
	}
	b, rerr := os.ReadFile(cachePath)
	if rerr != nil {
		return nil, fmt.Errorf("discover: %w (no usable cache)", err)
	}
	var cached []store.Unit
	if jerr := json.Unmarshal(b, &cached); jerr != nil || len(cached) == 0 {
		return nil, fmt.Errorf("discover: %w (cache unusable)", err)
	}
	slog.Warn("discovery failed, using cache", "err", err)
	return cached, nil
}

func discoverLive(ctx context.Context, api API) ([]store.Unit, error) {
	raw, err := api.Get(ctx, "/sites/")
	if err != nil {
		return nil, err
	}
	sites, err := kumo.ParseSites(raw)
	if err != nil {
		return nil, err
	}
	var units []store.Unit
	for _, site := range sites {
		zraw, err := api.Get(ctx, "/sites/"+site.ID+"/zones")
		if err != nil {
			return nil, err
		}
		zones, err := kumo.ParseZones(zraw)
		if err != nil {
			return nil, err
		}
		for _, z := range zones {
			if z.Serial == "" {
				continue
			}
			units = append(units, store.Unit{Slug: store.Slug(z.Name), Name: z.Name, Serial: z.Serial, ZoneID: z.ID, SiteID: site.ID})
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("no units found")
	}
	return units, nil
}
