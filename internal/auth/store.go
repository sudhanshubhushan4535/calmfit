package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/analytics"
)

// User is the public shape returned by /me and /auth/google.
type User struct {
	ID    string  `json:"id"`
	Email string  `json:"email"`
	Name  *string `json:"name"`
}

// upsertUser creates or refreshes the user for this Google sub and returns its id.
// A deleted account's tombstone has google_sub NULL (users_tombstone_check), so it can never match:
// signing in again with that Google account creates a new user.
// A new user gets signup_completed in the same transaction (xmax = 0 only on a fresh insert).
func upsertUser(ctx context.Context, db *pgxpool.Pool, id Identity) (string, error) {
	var uid string
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var inserted bool
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (google_sub, email, name) VALUES ($1, $2, $3)
			ON CONFLICT (google_sub) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
			RETURNING id::text, xmax = 0`,
			id.Sub, id.Email, nullIfEmpty(id.Name)).Scan(&uid, &inserted); err != nil {
			return err
		}
		if inserted {
			return analytics.Record(ctx, tx, uid, analytics.SignupCompleted)
		}
		return nil
	})
	return uid, err
}

func createSession(ctx context.Context, db *pgxpool.Pool, hash []byte, userID string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO sessions (id_hash, user_id, expires_at)
		VALUES ($1, $2::uuid, now() + $3::interval)`,
		hash, userID, sessionTTLInterval)
	return err
}

// sessionUserID returns the user for a live session, or pgx.ErrNoRows.
func sessionUserID(ctx context.Context, db *pgxpool.Pool, hash []byte) (string, error) {
	var uid string
	err := db.QueryRow(ctx, `
		SELECT s.user_id::text
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = $1 AND s.expires_at > now() AND u.deleted_at IS NULL`,
		hash).Scan(&uid)
	return uid, err
}

func deleteSession(ctx context.Context, db *pgxpool.Pool, hash []byte) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE id_hash = $1`, hash)
	return err
}

func getUser(ctx context.Context, db *pgxpool.Pool, id string) (User, error) {
	var u User
	err := db.QueryRow(ctx, `
		SELECT id::text, email, name FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		id).Scan(&u.ID, &u.Email, &u.Name)
	return u, err
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
