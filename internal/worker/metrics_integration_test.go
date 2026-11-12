package worker

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// sums gathers reg and returns the total of each gauge family by name.
func sums(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			out[mf.GetName()] += m.GetGauge().GetValue()
		}
	}
	return out
}

func TestKafkaCollectorLagAndDLQ(t *testing.T) {
	k := testutil.KafkaTopics(t)
	cl, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// One key, so all 5 records land in one partition.
	var recs []*kgo.Record
	for range 5 {
		recs = append(recs, &kgo.Record{Topic: k.Topic, Key: []byte("u1"), Value: []byte("{}")})
	}
	recs = append(recs, &kgo.Record{Topic: k.DLQ, Value: []byte("bad")}, &kgo.Record{Topic: k.DLQ, Value: []byte("bad")})
	res := cl.ProduceSync(ctx, recs...)
	if err := res.FirstErr(); err != nil {
		t.Fatal(err)
	}
	// Results come back in completion order, not input order: take the partition from a main-topic record.
	var part int32 = -1
	for _, r := range res {
		if r.Record.Topic == k.Topic {
			part = r.Record.Partition
		}
	}

	adm := kadm.NewClient(cl)
	group := k.Topic + "-group"
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewKafkaCollector(adm, group, k.Topic, k.DLQ))

	got := sums(t, reg)
	if got["calmfit_kafka_scrape_ok"] != 1 {
		t.Fatalf("scrape_ok = %v", got["calmfit_kafka_scrape_ok"])
	}
	if got["calmfit_kafka_consumer_lag"] != 5 {
		t.Errorf("lag with nothing committed = %v, want 5", got["calmfit_kafka_consumer_lag"])
	}
	if got["calmfit_kafka_dlq_records"] != 2 {
		t.Errorf("dlq records = %v, want 2", got["calmfit_kafka_dlq_records"])
	}

	// Commit 3 of the 5: lag drops to 2.
	var offs kadm.Offsets
	offs.Add(kadm.Offset{Topic: k.Topic, Partition: part, At: 3, LeaderEpoch: -1})
	if err := adm.CommitAllOffsets(ctx, group, offs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = adm.DeleteGroups(context.Background(), group) })
	if lag := sums(t, reg)["calmfit_kafka_consumer_lag"]; lag != 2 {
		t.Errorf("lag after committing 3 = %v, want 2", lag)
	}
}
