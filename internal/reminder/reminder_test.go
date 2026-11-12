package reminder

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/progression"
	"github.com/sudhanshubhushan4535/calmfit/internal/workout"
)

func TestGap(t *testing.T) {
	for days, want := range map[int]int{2: 3, 3: 2, 4: 1, 5: 1, 6: 1} {
		if got := Gap(days); got != want {
			t.Errorf("Gap(%d) = %d, want %d", days, got, want)
		}
	}
}

func TestDue(t *testing.T) {
	ist, _ := time.LoadLocation("Asia/Kolkata")
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, ist)
	ago := func(n int) *time.Time { d := today.AddDate(0, 0, -n); return &d }
	cases := []struct {
		name         string
		last         *time.Time
		days         int
		startedToday bool
		want         bool
	}{
		{"never completed", nil, 3, false, true},
		{"trained today", ago(0), 6, false, false},
		{"3/wk, yesterday", ago(1), 3, false, false},
		{"3/wk, 2 days ago", ago(2), 3, false, true},
		{"4/wk, yesterday", ago(1), 4, false, true},
		{"2/wk, 2 days ago", ago(2), 2, false, false},
		{"2/wk, 3 days ago", ago(3), 2, false, true},
		{"long break", ago(30), 3, false, true},
		{"already started today", ago(5), 3, true, false},
	}
	for _, c := range cases {
		if got := Due(today, c.last, c.days, c.startedToday); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestText(t *testing.T) {
	plan := func(g int) *progression.Plan {
		return &progression.Plan{Sets: []progression.Target{{WeightG: g, Reps: 12}}}
	}
	cases := []struct {
		p    workout.Preview
		want string
	}{
		{workout.Preview{Day: "pull", Exercises: []workout.Exercise{
			{Name: "Lat Pulldown (shoulder-width grip)", Plan: plan(45000)},
			{Name: "Seated Cable Row", Plan: plan(40000)},
		}}, "Pull day today. Beat 45 kg on the lat pulldown."},
		// The first exercise without history is skipped; half kilos keep their decimal.
		{workout.Preview{Day: "push", Exercises: []workout.Exercise{
			{Name: "Incline Dumbbell Press"},
			{Name: "Machine Chest Fly (Pec Deck)", Plan: plan(37500)},
		}}, "Push day today. Beat 37.5 kg on the machine chest fly."},
		{workout.Preview{Day: "legs", Exercises: []workout.Exercise{{Name: "Barbell Squat"}}},
			"Legs day today. Your plan is ready."},
		{workout.Preview{Day: "push", Exercises: []workout.Exercise{{Name: "Dumbbell Lateral Raise", Plan: plan(1250)}}},
			"Push day today. Beat 1.25 kg on the dumbbell lateral raise."},
	}
	for _, c := range cases {
		if got := Text(c.p); got != c.want {
			t.Errorf("Text = %q, want %q", got, c.want)
		}
	}
}

func TestAllowedEndpoint(t *testing.T) {
	ok := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/QGx",
		"https://wns2-bl2p.notify.windows.com/w/?token=abc",
	}
	bad := []string{
		"http://fcm.googleapis.com/fcm/send/abc",      // not https
		"https://fcm.googleapis.com:8443/x",           // explicit port
		"https://user@fcm.googleapis.com/x",           // userinfo
		"https://evilfcm.googleapis.com.example.com/", // suffix trick
		"https://push.apple.com.evil.com/x",
		"https://127.0.0.1/x",
		"https://localhost/x",
		"https://metadata.google.internal/x",
		"not a url",
		"",
	}
	for _, u := range ok {
		if !AllowedEndpoint(u) {
			t.Errorf("rejected %s", u)
		}
	}
	for _, u := range bad {
		if AllowedEndpoint(u) {
			t.Errorf("accepted %s", u)
		}
	}
}

func TestValidateKeys(t *testing.T) {
	p256 := append([]byte{0x04}, make([]byte, 64)...)
	good := subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/x"}
	good.Keys.P256dh = base64.RawURLEncoding.EncodeToString(p256)
	good.Keys.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	if msg := validate(good); msg != "" {
		t.Fatalf("valid subscription rejected: %s", msg)
	}
	padded := good
	padded.Keys.Auth = base64.URLEncoding.EncodeToString(make([]byte, 16))
	if msg := validate(padded); msg != "" {
		t.Fatalf("padded auth rejected: %s", msg)
	}
	for name, mut := range map[string]func(*subscription){
		"compressed key": func(s *subscription) {
			s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(append([]byte{0x02}, make([]byte, 64)...))
		},
		"short key":     func(s *subscription) { s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(p256[:33]) },
		"auth 8 bytes":  func(s *subscription) { s.Keys.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 8)) },
		"not base64":    func(s *subscription) { s.Keys.Auth = "!!!" },
		"long endpoint": func(s *subscription) { s.Endpoint += strings.Repeat("a", 1024) },
	} {
		s := good
		mut(&s)
		if validate(s) == "" {
			t.Errorf("%s accepted", name)
		}
	}
}
