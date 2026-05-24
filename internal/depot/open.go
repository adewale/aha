package depot

import (
	"fmt"

	"github.com/adewale/aha/internal/model"
)

func Open(addr Address, cfg model.DepotConfig) (Driver, error) {
	switch addr.Type {
	case "", "local":
		return NewLocal(addr.Location)
	case "r2":
		r2cfg, err := ResolveR2Config(cfg.R2)
		if err != nil {
			return nil, err
		}
		return NewR2(addr.Location, r2cfg), nil
	default:
		return nil, fmt.Errorf("unsupported depot type %q", addr.Type)
	}
}
