package logstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	pool     *pgxpool.Pool
	service  *Service
	logger   *slog.Logger
	interval time.Duration
}

func NewWorker(pool *pgxpool.Pool, service *Service, logger *slog.Logger) *Worker {
	return &Worker{pool: pool, service: service, logger: logger, interval: time.Minute}
}

func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	if w.pool == nil || w.service == nil {
		return
	}
	rows, err := w.pool.Query(ctx, `SELECT id, COALESCE(s3_prefix,''), COALESCE(config_json->>'rotate_after','24h') FROM data_objects WHERE kind='log' AND status='active'`)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("log rotate list", "error", err)
		}
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, prefix, rotateAfter string
		if err := rows.Scan(&id, &prefix, &rotateAfter); err != nil {
			continue
		}
		d, err := time.ParseDuration(rotateAfter)
		if err != nil || d <= 0 {
			d = 24 * time.Hour
		}
		cutoff := time.Now().UTC().Add(-d)
		err = w.service.RotateOlderThan(ctx, id, cutoff, prefix,
			func(ctx context.Context, sql string, args ...any) error {
				_, err := w.pool.Exec(ctx, sql, args...)
				return err
			},
			func(ctx context.Context, sql string, args ...any) ([]Entry, error) {
				r, err := w.pool.Query(ctx, sql, args...)
				if err != nil {
					return nil, err
				}
				defer r.Close()
				out := []Entry{}
				for r.Next() {
					var e Entry
					var raw string
					if err := r.Scan(&e.ID, &e.Level, &e.Message, &raw, &e.CreatedAt); err != nil {
						return nil, err
					}
					e.Fields = map[string]any{}
					_ = json.Unmarshal([]byte(raw), &e.Fields)
					out = append(out, e)
				}
				return out, r.Err()
			},
		)
		if err != nil && w.logger != nil {
			w.logger.Warn("log rotate", "object", id, "error", err)
		}
	}
}
