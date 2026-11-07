package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrLastAdmin means the change would leave the room without an admin.
var ErrLastAdmin = errors.New("last admin")

// SetAdmin promotes or demotes a member. Demoting the only admin returns
// ErrLastAdmin.
func SetAdmin(ctx context.Context, roomID, memberID string, isAdmin bool) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		if !isAdmin {
			if err := ensureOtherAdmin(ctx, tx, roomID, memberID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE members SET is_admin = $3 WHERE id = $2 AND room_id = $1`, roomID, memberID, isAdmin)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// RemoveMember deletes a member, which revokes their token and deletes their
// votes and comments (their items stay). Removing the only admin returns
// ErrLastAdmin.
func RemoveMember(ctx context.Context, roomID, memberID string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		if err := ensureOtherAdmin(ctx, tx, roomID, memberID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM members WHERE id = $2 AND room_id = $1`, roomID, memberID)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// ResetPasscode stores a new passcode hash. Existing members stay joined.
func ResetPasscode(ctx context.Context, roomID, passcodeHash string) (time.Time, error) {
	var changedAt time.Time
	err := execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE rooms SET passcode_hash = $2, passcode_changed_at = clock_timestamp()
			WHERE id = $1
			RETURNING passcode_changed_at
		`, roomID, passcodeHash).Scan(&changedAt)
	})
	return changedAt, err
}

// ensureOtherAdmin returns ErrLastAdmin if memberID is an admin and no other
// member is. Members rows are locked so two admins can't demote each other at
// the same moment.
func ensureOtherAdmin(ctx context.Context, tx pgx.Tx, roomID, memberID string) error {
	rows, err := tx.Query(ctx, `
		SELECT id, is_admin FROM members WHERE room_id = $1 FOR UPDATE
	`, roomID)
	if err != nil {
		return err
	}
	type m struct {
		id      string
		isAdmin bool
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (m, error) {
		var x m
		err := row.Scan(&x.id, &x.isAdmin)
		return x, err
	})
	if err != nil {
		return err
	}

	targetIsAdmin, otherAdmins := false, 0
	for _, x := range members {
		if x.id == memberID {
			targetIsAdmin = x.isAdmin
		} else if x.isAdmin {
			otherAdmins++
		}
	}
	if targetIsAdmin && otherAdmins == 0 {
		return ErrLastAdmin
	}
	return nil
}
