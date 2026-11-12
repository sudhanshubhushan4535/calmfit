package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// OutboxCollector reports the outbox backlog at scrape time. The query reads only unpublished
// rows, which the partial index outbox_unpublished_idx keeps small.
type OutboxCollector struct {
	DB *pgxpool.Pool

	unpublished, oldest, up *prometheus.Desc
}

// NewOutboxCollector returns a collector for db's outbox.
func NewOutboxCollector(db *pgxpool.Pool) *OutboxCollector {
	return &OutboxCollector{
		DB:          db,
		unpublished: prometheus.NewDesc("calmfit_outbox_unpublished", "Outbox rows not yet published to Kafka.", nil, nil),
		oldest:      prometheus.NewDesc("calmfit_outbox_oldest_unpublished_seconds", "Age of the oldest unpublished outbox row (0 when none).", nil, nil),
		up:          prometheus.NewDesc("calmfit_outbox_scrape_ok", "1 if the outbox query succeeded on this scrape.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *OutboxCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.unpublished
	ch <- c.oldest
	ch <- c.up
}

// Collect implements prometheus.Collector. A failed query reports calmfit_outbox_scrape_ok 0
// instead of failing the whole scrape, so API latency metrics survive a DB problem.
func (c *OutboxCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var n int64
	var age float64
	err := c.DB.QueryRow(ctx, `
		SELECT count(*), coalesce(extract(epoch FROM now() - min(created_at)), 0)::float8
		FROM outbox WHERE published_at IS NULL`).Scan(&n, &age)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(c.unpublished, prometheus.GaugeValue, float64(n))
	ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, age)
}
