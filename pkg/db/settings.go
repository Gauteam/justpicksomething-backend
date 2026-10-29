package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrBelowCount means max_items would be lower than the options already in
// the room.
var ErrBelowCount = errors.New("limit below current count")

// SettingsPatch holds the settings to change; nil fields are left alone.
type SettingsPatch struct {
	Name *string
	// SetMaxItems says whether to change MaxItems (nil MaxItems = no limit).
	SetMaxItems         bool
	MaxItems            *int
	VoteMode            *string
	VotesPerMember      *int
	AllowDuplicateVotes *bool
	Locked              *bool
}

// UpdateSettings applies p to the room. Switching vote mode clears every vote
// (thumbs and budget votes don't mean the same thing); turning off duplicate
// votes caps each member's votes per option at one.
func UpdateSettings(ctx context.Context, room Room, p SettingsPatch) error {
	return execInRoom(ctx, room.ID, func(tx pgx.Tx) error {
		if p.SetMaxItems && p.MaxItems != nil {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM items WHERE room_id = $1`, room.ID).Scan(&count); err != nil {
				return err
			}
			if count > *p.MaxItems {
				return ErrBelowCount
			}
		}

		_, err := tx.Exec(ctx, `
			UPDATE rooms SET
				name = COALESCE($2, name),
				max_items = CASE WHEN $3 THEN $4 ELSE max_items END,
				vote_mode = COALESCE($5, vote_mode),
				votes_per_member = COALESCE($6, votes_per_member),
				allow_duplicate_votes = COALESCE($7, allow_duplicate_votes),
				locked_at = CASE
					WHEN $8::boolean IS NULL THEN locked_at
					WHEN $8 THEN COALESCE(locked_at, now())
					ELSE NULL
				END
			WHERE id = $1
		`, room.ID, p.Name, p.SetMaxItems, p.MaxItems, p.VoteMode, p.VotesPerMember, p.AllowDuplicateVotes, p.Locked)
		if err != nil {
			return err
		}

		if p.VoteMode != nil && *p.VoteMode != room.VoteMode {
			_, err = tx.Exec(ctx, `DELETE FROM votes WHERE room_id = $1`, room.ID)
		} else if p.AllowDuplicateVotes != nil && !*p.AllowDuplicateVotes {
			_, err = tx.Exec(ctx, `UPDATE votes SET value = 1 WHERE room_id = $1 AND value > 1`, room.ID)
		}
		return err
	})
}
