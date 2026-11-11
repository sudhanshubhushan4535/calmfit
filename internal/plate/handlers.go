package plate

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/httpx"
)

const (
	maxBodyBytes = 1 << 10
	// EditableDays is how far back a plate check can be logged or edited: today and the 7 days before.
	EditableDays = 7
	// MaxRangeDays caps the range of GET /plate.
	MaxRangeDays = 31
	// StreakLookbackDays bounds the streak query, so a streak reports at most this many days.
	StreakLookbackDays = 366
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Handler serves the plate-check endpoints. Every query is scoped to the session user.
type Handler struct {
	DB  *pgxpool.Pool
	Cfg config.Config
	Now func() time.Time // nil means time.Now
}

// Register adds the routes; every route is wrapped in requireUser.
func (h *Handler) Register(mux *http.ServeMux, requireUser func(http.Handler) http.Handler) {
	mux.Handle("PUT /plate/{date}/{meal}", requireUser(http.HandlerFunc(h.put)))
	mux.Handle("GET /plate", requireUser(http.HandlerFunc(h.get)))
}

func (h *Handler) today() time.Time {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	return h.Cfg.Today(now())
}

// Summary is the response of GET /plate.
type Summary struct {
	Today      Day   `json:"today"`
	Streak     int   `json:"streak"` // consecutive good days; an unfinished today doesn't break it
	GoodDayMin int   `json:"good_day_min"`
	Days       []Day `json:"days"` // from..to, oldest first, every date present
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, s)
	return t, err == nil
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()

	date, ok := parseDate(r.PathValue("date"))
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	today := h.today()
	if date.After(today) || date.Before(today.AddDate(0, 0, -EditableDays)) {
		httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("date must be today or up to %d days ago (local)", EditableDays))
		return
	}
	meal := r.PathValue("meal")
	if !ValidMeal(meal) {
		httpx.WriteError(w, http.StatusBadRequest, "meal must be breakfast, lunch or dinner")
		return
	}
	var body struct {
		Protein *bool `json:"protein"`
		Fiber   *bool `json:"fiber"`
	}
	if !httpx.DecodeJSON(w, r, maxBodyBytes, &body) {
		return
	}
	if body.Protein == nil || body.Fiber == nil {
		httpx.WriteError(w, http.StatusBadRequest, `send {"protein": true|false, "fiber": true|false}`)
		return
	}

	// Upsert: one row per (user, date, meal), so retries and edits never duplicate.
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO plate_checks (user_id, local_date, meal, protein, fiber) VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (user_id, local_date, meal)
		DO UPDATE SET protein = EXCLUDED.protein, fiber = EXCLUDED.fiber, updated_at = now()`,
		uid, date, meal, *body.Protein, *body.Fiber); err != nil {
		httpx.InternalError(w, "plate: upsert", err)
		return
	}
	days, err := Days(ctx, h.DB, uid, date, date, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "plate: load day", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, days[0])
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	uid, _ := auth.UserID(r.Context())
	ctx := r.Context()
	today := h.today()

	to, from := today, today.AddDate(0, 0, -6)
	if s := r.URL.Query().Get("to"); s != "" {
		var ok bool
		if to, ok = parseDate(s); !ok {
			httpx.WriteError(w, http.StatusBadRequest, "to must be YYYY-MM-DD")
			return
		}
		from = to.AddDate(0, 0, -6)
	}
	if s := r.URL.Query().Get("from"); s != "" {
		var ok bool
		if from, ok = parseDate(s); !ok {
			httpx.WriteError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
			return
		}
	}
	if from.After(to) || to.After(today) || to.Sub(from) >= MaxRangeDays*24*time.Hour {
		httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("need from <= to <= today, at most %d days", MaxRangeDays))
		return
	}

	days, err := Days(ctx, h.DB, uid, from, to, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "plate: load days", err)
		return
	}
	todays, err := Days(ctx, h.DB, uid, today, today, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "plate: load today", err)
		return
	}
	streak, err := StreakAt(ctx, h.DB, uid, today, true, h.Cfg.Plate)
	if err != nil {
		httpx.InternalError(w, "plate: streak", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Summary{Today: todays[0], Streak: streak, GoodDayMin: h.Cfg.Plate.GoodDayMin, Days: days})
}

// load returns the user's checks in [from, to], keyed by date then meal.
func load(ctx context.Context, q Querier, uid string, from, to time.Time) (map[time.Time]map[string]Check, error) {
	rows, err := q.Query(ctx, `
		SELECT local_date, meal, protein, fiber FROM plate_checks
		WHERE user_id = $1::uuid AND local_date BETWEEN $2 AND $3`, uid, from, to)
	if err != nil {
		return nil, fmt.Errorf("query plate checks: %w", err)
	}
	defer rows.Close()
	out := map[time.Time]map[string]Check{}
	for rows.Next() {
		var d time.Time
		var meal string
		var c Check
		if err := rows.Scan(&d, &meal, &c.Protein, &c.Fiber); err != nil {
			return nil, fmt.Errorf("scan plate check: %w", err)
		}
		d = config.Date(d)
		if out[d] == nil {
			out[d] = map[string]Check{}
		}
		out[d][meal] = c
	}
	return out, rows.Err()
}

// Days returns a scored Day for every date in [from, to] (config.Date values), oldest first.
func Days(ctx context.Context, q Querier, uid string, from, to time.Time, cfg config.Plate) ([]Day, error) {
	checks, err := load(ctx, q, uid, from, to)
	if err != nil {
		return nil, err
	}
	var out []Day
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, Score(d, checks[d], cfg))
	}
	return out, nil
}

// StreakAt returns the good-day streak ending at end (see Streak), looking back at most StreakLookbackDays.
func StreakAt(ctx context.Context, q Querier, uid string, end time.Time, endInProgress bool, cfg config.Plate) (int, error) {
	days, err := Days(ctx, q, uid, end.AddDate(0, 0, -(StreakLookbackDays-1)), end, cfg)
	if err != nil {
		return 0, err
	}
	good := map[time.Time]bool{}
	for _, d := range days {
		if d.Good {
			t, _ := parseDate(d.Date)
			good[t] = true
		}
	}
	return Streak(good, end, endInProgress), nil
}

// GoodDays counts good days in [from, to].
func GoodDays(days []Day) int {
	n := 0
	for _, d := range days {
		if d.Good {
			n++
		}
	}
	return n
}
