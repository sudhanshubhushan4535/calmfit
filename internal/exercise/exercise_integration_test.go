package exercise_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudhanshubhushan4535/calmfit/internal/exercise"
	"github.com/sudhanshubhushan4535/calmfit/internal/server"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

func TestHowTo(t *testing.T) {
	db := testutil.DB(t)
	prefix := testutil.SubPrefix(t, db)
	fv := testutil.NewFakeVerifier()
	srv := httptest.NewServer(server.New(db, fv, "test-client-id", testutil.Config(t)))
	t.Cleanup(srv.Close)
	c := testutil.Login(t, srv.URL, fv, prefix+"howto")

	get := func(c *http.Client, path string) (int, []byte) {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	status, b := get(c, "/exercises/Barbell_Squat")
	if status != http.StatusOK {
		t.Fatalf("get: %d %s", status, b)
	}
	var x exercise.HowTo
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	if len(x.Instructions) == 0 || len(x.Cues) != 2 || x.CuesStatus != "review" || len(x.Images) != 2 {
		t.Fatalf("how-to: %+v", x)
	}
	for _, img := range x.Images {
		if !strings.HasPrefix(img, "/static/exercises/Barbell_Squat/") {
			t.Fatalf("image not local: %s", img)
		}
		// Served publicly from our own static folder.
		if status, _ := get(http.DefaultClient, img); status != http.StatusOK {
			t.Fatalf("image %s: %d", img, status)
		}
	}

	if status, _ := get(c, "/exercises/Nope"); status != http.StatusNotFound {
		t.Fatalf("unknown exercise: %d, want 404", status)
	}
	if status, _ := get(http.DefaultClient, "/exercises/Barbell_Squat"); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d, want 401", status)
	}
}
