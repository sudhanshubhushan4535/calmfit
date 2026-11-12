package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/events"
	"github.com/sudhanshubhushan4535/calmfit/internal/outbox"
	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// An enqueued event shares the caller's transaction: rolled back together, committed together.
func TestEnqueueIsTransactional(t *testing.T) {
	db := testutil.DB(t)
	uid := newUser(t, db, testutil.SubPrefix(t, db)+"tx")
	ctx := context.Background()
	boom := errors.New("domain write failed")

	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := outbox.Enqueue(ctx, tx, uid, events.TypeUserDeleted, events.V1, events.UserDeleted{}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := unpublished(t, db, uid); n != 0 {
		t.Fatalf("rows after rollback = %d, want 0", n)
	}

	enqueue(t, db, uid, 1)
	if n := unpublished(t, db, uid); n != 1 {
		t.Fatalf("rows after commit = %d, want 1", n)
	}
}
