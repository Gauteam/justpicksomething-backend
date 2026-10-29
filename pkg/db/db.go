package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	pool    *pgxpool.Pool
	poolErr error
	once    sync.Once
)

// Pool returns the shared connection pool, creating it on first use. The pool
// outlives individual requests so warm serverless invocations reuse it.
func Pool() (*pgxpool.Pool, error) {
	once.Do(func() {
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			poolErr = errors.New("DATABASE_URL is not set")
			return
		}

		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			poolErr = err
			return
		}

		// Neon + serverless tuning: few connections, close idle ones quickly so
		// the Neon compute can suspend.
		cfg.MaxConns = 2
		cfg.MinConns = 0
		cfg.MaxConnLifetime = 15 * time.Minute
		cfg.MaxConnIdleTime = 2 * time.Minute
		cfg.HealthCheckPeriod = 30 * time.Second

		pool, poolErr = pgxpool.NewWithConfig(context.Background(), cfg)
	})

	return pool, poolErr
}

// Ping checks that the database is reachable.
func Ping(ctx context.Context) error {
	p, err := Pool()
	if err != nil {
		return err
	}
	return p.Ping(ctx)
}
