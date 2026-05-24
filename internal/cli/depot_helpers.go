package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/adewale/aha/internal/depot"
	"github.com/adewale/aha/internal/model"
)

func depotDriverForConfig(cfg model.Config, override string) (depot.Driver, error) {
	addr := depot.AddressFromConfig(cfg.Depot)
	if override != "" {
		parsed, err := depot.ParseAddress(override)
		if err != nil {
			return nil, err
		}
		addr = parsed
	}
	cfg.Depot.Type = addr.Type
	cfg.Depot.Location = addr.Location
	return depot.Open(addr, cfg.Depot)
}

func localDepotBundlePath(d depot.Driver, ref depot.BundleRef) string {
	if d.Address().Type != "local" {
		return ""
	}
	key := ref.Key
	if key == "" {
		key = depot.BundleKey(ref.BundleSHA256)
	}
	if err := depot.ValidateBundleKey(key); err != nil {
		return ""
	}
	return filepath.Join(d.Address().Location, filepath.FromSlash(key))
}

func ensureDepot(ctx context.Context, d depot.Driver) error {
	if d == nil {
		return fmt.Errorf("depot not configured")
	}
	return d.Init(ctx)
}
