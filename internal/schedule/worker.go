package schedule

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
	return &Worker{pool: pool, service: service, logger: logger, interval: 15 * time.Second}
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
	now := time.Now().UTC()
	if w.pool == nil {
		_ = w.service.FireDueMemory(now)
		return
	}
	rows, err := w.pool.Query(ctx, `SELECT object_id, name, cron_expr, task, data::text, next_run_at FROM schedule_entries WHERE enabled=true AND next_run_at <= $1`, now)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("schedule worker query", "error", err)
		}
		return
	}
	defer rows.Close()
	for rows.Next() {
		var objectID, name, cronExpr, task, raw string
		var next time.Time
		if err := rows.Scan(&objectID, &name, &cronExpr, &task, &raw, &next); err != nil {
			continue
		}
		data := map[string]any{}
		_ = json.Unmarshal([]byte(raw), &data)
		_, _ = w.pool.Exec(ctx, `INSERT INTO schedule_deliveries (object_id, entry_name, run_at, task, data, created_at)
VALUES ($1,$2,$3,$4,$5::jsonb,$6)`, objectID, name, now, task, string(raw), now)
		newNext := nextFromCron(cronExpr, now)
		_, _ = w.pool.Exec(ctx, `UPDATE schedule_entries SET next_run_at=$1, updated_at=$2 WHERE object_id=$3 AND name=$4`, newNext, now, objectID, name)
		w.service.PushMemory(Delivery{ObjectID: objectID, EntryName: name, RunAt: now, Task: task, Data: data})
	}
}
