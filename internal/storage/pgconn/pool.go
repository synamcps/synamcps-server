package pgconn

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synamcps/synamcps-server/internal/config"
)

func NewPool(ctx context.Context, cfg config.MetadataConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	maxConns := cfg.PoolMaxConns
	if maxConns <= 0 {
		maxConns = 20
	}
	minConns := cfg.PoolMinConns
	if minConns <= 0 {
		minConns = 2
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = minConns
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	return pool, nil
}
