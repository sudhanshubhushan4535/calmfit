package plate_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// Every plate-check upsert writes one plate.checked row carrying the meal's new state.
func TestPutWritesPlateCheckedEvent(t *testing.T) {
	e := setup(t)
	c := testutil.Login(t, e.url, e.fv, e.prefix+"outbox")
	e.put(t, c, 1, "lunch", true, false)
	e.put(t, c, 1, "lunch", true, true)

	rows, err := e.db.Query(context.Background(), `
		SELECT o.type, o.version, o.payload FROM outbox o JOIN users u ON u.id = o.user_id
		WHERE u.google_sub = $1 ORDER BY o.id`, e.prefix+"outbox")
	if err != nil {
		t.Fatal(err)
	}
	var got []events.PlateChecked
	for rows.Next() {
		var typ string
		var version int
		var payload []byte
		if err := rows.Scan(&typ, &version, &payload); err != nil {
			t.Fatal(err)
		}
		if typ != events.TypePlateChecked || version != 1 {
			t.Fatalf("event %s v%d", typ, version)
		}
		var p events.PlateChecked
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := events.PlateChecked{LocalDate: e.date(1), Meal: "lunch", Protein: true, Fiber: true}
	if len(got) != 2 || got[1] != want || got[0].Fiber {
		t.Fatalf("events = %+v, want 2 ending with %+v", got, want)
	}
}
