package worker

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kadm"
)

// Event counters, incremented in Handle. They live at package level so Handle needs no extra
// field; RegisterMetrics exposes them on a registry.
var (
	eventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "calmfit_worker_events_total",
		Help: "Records handled by this worker by outcome (applied, duplicate, skipped_user, dlq).",
	}, []string{"outcome"})
	retriesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "calmfit_worker_retries_total",
		Help: "Failed attempts that were retried in place (transient errors).",
	})
)

// OutcomeDLQ labels records this worker sent to the DLQ.
const OutcomeDLQ Outcome = "dlq"

// RegisterMetrics registers the worker's counters on reg.
func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(eventsTotal, retriesTotal)
}

// KafkaCollector reports consumer lag and the DLQ size, read from the broker at scrape time.
// Every worker instance reports the same group-wide numbers, so dashboards take max().
type KafkaCollector struct {
	Admin *kadm.Client
	Group string
	Topic string
	DLQ   string

	lag, dlq, up *prometheus.Desc
}

// NewKafkaCollector returns a collector for group's lag on topic and the size of dlq.
func NewKafkaCollector(adm *kadm.Client, group, topic, dlq string) *KafkaCollector {
	return &KafkaCollector{
		Admin: adm, Group: group, Topic: topic, DLQ: dlq,
		lag: prometheus.NewDesc("calmfit_kafka_consumer_lag",
			"Records not yet committed by the consumer group (end offset − committed offset; log start when nothing is committed).",
			[]string{"group", "topic", "partition"}, nil),
		dlq: prometheus.NewDesc("calmfit_kafka_dlq_records",
			"Records currently in the DLQ topic (Σ end − start offsets).", []string{"topic"}, nil),
		up: prometheus.NewDesc("calmfit_kafka_scrape_ok", "1 if the broker answered on this scrape.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *KafkaCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lag
	ch <- c.dlq
	ch <- c.up
}

// Collect implements prometheus.Collector. A broker error reports calmfit_kafka_scrape_ok 0
// instead of failing the scrape.
func (c *KafkaCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lags, dlq, err := c.read(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
	for p, n := range lags {
		ch <- prometheus.MustNewConstMetric(c.lag, prometheus.GaugeValue, float64(n), c.Group, c.Topic, strconv.Itoa(int(p)))
	}
	ch <- prometheus.MustNewConstMetric(c.dlq, prometheus.GaugeValue, float64(dlq), c.DLQ)
}

// read returns the lag per partition of Topic and the record count of DLQ.
func (c *KafkaCollector) read(ctx context.Context) (map[int32]int64, int64, error) {
	ends, err := c.Admin.ListEndOffsets(ctx, c.Topic, c.DLQ)
	if err == nil {
		err = ends.Error()
	}
	if err != nil {
		return nil, 0, err
	}
	starts, err := c.Admin.ListStartOffsets(ctx, c.Topic, c.DLQ)
	if err == nil {
		err = starts.Error()
	}
	if err != nil {
		return nil, 0, err
	}
	committed, err := c.Admin.FetchOffsets(ctx, c.Group)
	if err != nil {
		return nil, 0, err
	}

	lags := map[int32]int64{}
	var dlq int64
	ends.Each(func(o kadm.ListedOffset) {
		start := int64(0)
		if s, ok := starts.Lookup(o.Topic, o.Partition); ok {
			start = s.Offset
		}
		switch o.Topic {
		case c.DLQ:
			dlq += o.Offset - start
		case c.Topic:
			at := start
			if cm, ok := committed.Lookup(o.Topic, o.Partition); ok && cm.At >= start {
				at = cm.At
			}
			lags[o.Partition] = max(o.Offset-at, 0)
		}
	})
	return lags, dlq, nil
}
