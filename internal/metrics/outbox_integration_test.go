package metrics

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

func gauge(t *testing.T, reg *prometheus.Registry, name string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name && len(mf.GetMetric()) == 1 {
			return mf.GetMetric()[0].GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func TestOutboxCollectorReportsBacklog(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	var id int64
	// outbox.user_id has no FK, so a random user id is fine. Backdated so the age is visible.
	if err := db.QueryRow(ctx, `
		INSERT INTO outbox (user_id, type, version, payload, created_at)
		VALUES (gen_random_uuid(), 'metrics.test', 1, '{}', now() - interval '1 hour')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, id) })

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewOutboxCollector(db))
	if ok, _ := gauge(t, reg, "calmfit_outbox_scrape_ok"); ok != 1 {
		t.Fatalf("scrape_ok = %v, want 1", ok)
	}
	if n, _ := gauge(t, reg, "calmfit_outbox_unpublished"); n < 1 {
		t.Errorf("unpublished = %v, want >= 1", n)
	}
	if age, _ := gauge(t, reg, "calmfit_outbox_oldest_unpublished_seconds"); age < 3600 {
		t.Errorf("oldest age = %v s, want >= 3600", age)
	}
}

func TestOutboxCollectorDBDownReportsNotOK(t *testing.T) {
	db := testutil.DB(t)
	c := NewOutboxCollector(db)
	db.Close() // a closed pool fails every query
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	if ok, found := gauge(t, reg, "calmfit_outbox_scrape_ok"); !found || ok != 0 {
		t.Fatalf("scrape_ok = %v (found %v), want 0", ok, found)
	}
	if _, found := gauge(t, reg, "calmfit_outbox_unpublished"); found {
		t.Error("unpublished reported although the query failed")
	}
}
