package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"time"

	"context"

	"github.com/sudhanshubhushan4535/calmfit/internal/auth"
	"github.com/sudhanshubhushan4535/calmfit/internal/ratelimit"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
)

// redisDrill runs the real router in-process with the real DB and the real Redis limiter, but a
// fake voice parser, so the drill never calls the LLM and the production binary never gets a fake
// parser mode (D42). It sends n requests to each endpoint and checks the statuses:
// voice must be 200 with Redis up and 503 (fail closed) with Redis down; everything else 200.
func redisDrill(args []string) error {
	fs := flag.NewFlagSet("redis", flag.ExitOnError)
	phase := fs.String("phase", "up", "up or down: the Redis state the script set")
	n := fs.Int("n", 20, "requests per endpoint")
	sub := fs.String("user", "synthetic-00008", "synthetic user to sign in (make seed-synthetic)")
	_ = fs.Parse(args)
	if *phase != "up" && *phase != "down" {
		return errors.New("-phase must be up or down")
	}
	ctx := context.Background()
	p := db(ctx)
	defer p.Close()
	cfg := appConfig()
	cfg.Voice.Enabled, cfg.Voice.Model, cfg.Voice.DailyLimit = true, "fake", 1000

	rdb, err := ratelimit.NewClient(env("REDIS_URL"))
	if err != nil {
		return err
	}
	defer rdb.Close()
	parser := &voice.FakeParser{Results: map[string]voice.Parsed{}}
	limiter := ratelimit.NewDaily(rdb, "drill-voice", cfg.Voice.DailyLimit, cfg) // own key prefix: dev counters untouched
	srv := httptest.NewServer(server.New(p, auth.TestVerifier{Next: auth.GoogleVerifier{}}, "", cfg, server.WithVoice(parser, limiter)))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if code, body := call(c, "POST", srv.URL+"/auth/google", `{"credential":"test:`+*sub+`"}`); code != 200 {
		return fmt.Errorf("login %s: %d %s", *sub, code, body)
	}
	code, body := call(c, "POST", srv.URL+"/workouts/start", "")
	if code != 200 && code != 201 {
		return fmt.Errorf("start: %d %s", code, body)
	}
	var w struct {
		ID        string `json:"id"`
		Exercises []struct {
			ID   string            `json:"id"`
			Plan *json.RawMessage  `json:"plan"`
			Sets []json.RawMessage `json:"sets"`
			Rem  []struct {
				WeightG int `json:"weight_g"`
				Reps    int `json:"reps"`
			} `json:"remaining"`
		} `json:"exercises"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return err
	}
	// One planned exercise for tap logging (set 1, an idempotent upsert) and another with a set
	// left for the voice parse (read-only).
	tap, spoken := -1, -1
	for i, e := range w.Exercises {
		switch {
		case e.Plan == nil:
		case tap < 0:
			tap = i
		case spoken < 0 && len(e.Sets) < 3:
			spoken = i
		}
	}
	if tap < 0 || spoken < 0 {
		return errors.New("the drill user's workout needs two planned exercises")
	}
	parser.Results["drill: ten reps"] = voice.Parsed{ExerciseID: w.Exercises[spoken].ID, Sets: []voice.ParsedSet{{Reps: 10}}}
	t := w.Exercises[tap]
	set1 := fmt.Sprintf(`{"exercise_id":%q,"set_number":1,"weight_g":%d,"reps":%d}`, t.ID, 20000, 10)

	type ep struct {
		name, method, path, body string
		want                     int
	}
	voiceWant := 200
	if *phase == "down" {
		voiceWant = 503
	}
	eps := []ep{
		{"voice parse", "POST", "/workouts/" + w.ID + "/voice/parse", `{"transcript":"drill: ten reps"}`, voiceWant},
		{"log a set", "POST", "/workouts/" + w.ID + "/sets", set1, 200},
		{"next workout", "GET", "/workouts/next", "", 200},
		{"weekly review", "GET", "/review/weekly", "", 200},
		{"history", "GET", "/progress/Barbell_Squat", "", 200},
	}
	fmt.Printf("Redis %s, %d requests each (user %s):\n", *phase, *n, *sub)
	fmt.Printf("  %-14s %-10s %8s %8s  %s\n", "endpoint", "want", "p50", "max", "statuses")
	failed := false
	for _, e := range eps {
		codes := map[int]int{}
		var lat []time.Duration
		for range *n {
			start := time.Now()
			code, _ := call(c, e.method, srv.URL+e.path, e.body)
			lat = append(lat, time.Since(start))
			codes[code]++
		}
		slices.Sort(lat)
		ok := codes[e.want] == *n
		failed = failed || !ok
		mark := "ok"
		if !ok {
			mark = "FAIL"
		}
		fmt.Printf("  %-14s %-10d %8s %8s  %v %s\n", e.name, e.want, lat[len(lat)/2].Round(100*time.Microsecond),
			lat[len(lat)-1].Round(100*time.Microsecond), codes, mark)
	}
	if failed {
		fmt.Println("RESULT: FAIL")
		return errors.New("unexpected statuses")
	}
	fmt.Println("RESULT: PASS")
	return nil
}

func call(c *http.Client, method, url, body string) (int, []byte) {
	req, _ := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
