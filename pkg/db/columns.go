package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrLimit = errors.New("limit reached")
	// ErrStale means the client's list doesn't match what's in the room, e.g.
	// someone added an item while it was being reordered.
	ErrStale = errors.New("stale list")
)

type Column struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Position int    `json:"position"`
}

type Comment struct {
	ID        string     `json:"id"`
	ItemID    string     `json:"itemId"`
	AuthorID  string     `json:"authorId"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"createdAt"`
	EditedAt  *time.Time `json:"editedAt"`
}

// Columns lists a room's columns in display order.
func Columns(ctx context.Context, roomID string) ([]Column, error) {
	p, err := Pool()
	if err != nil {
		return nil, err
	}

	rows, err := p.Query(ctx, `
		SELECT id, name, type, position
		FROM columns
		WHERE room_id = $1
		ORDER BY position, created_at
	`, roomID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		var c Column
		err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Position)
		return c, err
	})
}

// AddColumn appends a column. It returns ErrLimit if the room already has max
// columns and ErrNameTaken if the name is in use (ignoring case).
func AddColumn(ctx context.Context, roomID, name, typ string, max int) (Column, error) {
	p, err := Pool()
	if err != nil {
		return Column{}, err
	}

	var c Column
	err = pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO columns (room_id, name, type, position)
			SELECT $1, $2, $3, COALESCE(max(position), 0) + 1
			FROM columns
			WHERE room_id = $1
			HAVING count(*) < $4
			RETURNING id, name, type, position
		`, roomID, name, typ, max).Scan(&c.ID, &c.Name, &c.Type, &c.Position)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLimit
		}
		if isUniqueViolation(err, "columns_room_name") {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		return touchRoom(ctx, tx, roomID)
	})
	return c, err
}

// RenameColumn renames a column.
func RenameColumn(ctx context.Context, roomID, columnID, name string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE columns SET name = $3 WHERE id = $2 AND room_id = $1`, roomID, columnID, name)
		if isUniqueViolation(err, "columns_room_name") {
			return ErrNameTaken
		}
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// DeleteColumn deletes a column and its values on every item.
func DeleteColumn(ctx context.Context, roomID, columnID string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM columns WHERE id = $2 AND room_id = $1`, roomID, columnID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `
			UPDATE items SET fields = fields - $2::text
			WHERE room_id = $1 AND fields ? $2::text
		`, roomID, columnID)
		return err
	})
}

// ReorderColumns sets column positions to the order of ids, which must list
// every column in the room exactly once.
func ReorderColumns(ctx context.Context, roomID string, ids []string) error {
	return reorder(ctx, "columns", roomID, ids)
}

// ReorderItems sets the manual item order; ids must list every item in the
// room exactly once.
func ReorderItems(ctx context.Context, roomID string, ids []string) error {
	return reorder(ctx, "items", roomID, ids)
}

func reorder(ctx context.Context, table, roomID string, ids []string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE room_id = $1`, table), roomID).Scan(&count); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s AS t SET position = o.ord
			FROM unnest($2::uuid[]) WITH ORDINALITY AS o(id, ord)
			WHERE t.id = o.id AND t.room_id = $1
		`, table), roomID, ids)
		if err != nil {
			return err
		}
		if count != len(ids) || tag.RowsAffected() != int64(len(ids)) {
			return ErrStale
		}
		return nil
	})
}

// UpdateItem changes an item's title (if title is non-nil) and merges fields
// into its column values; a nil value in fields clears that column. It
// returns ErrNameTaken if another item in the room has the new title.
func UpdateItem(ctx context.Context, roomID, itemID string, title *string, fields map[string]any) error {
	patch, err := json.Marshal(fields)
	if err != nil {
		return err
	}

	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		if title != nil {
			var taken bool
			err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM items WHERE room_id = $1 AND id <> $2 AND lower(title) = lower($3)
				)
			`, roomID, itemID, *title).Scan(&taken)
			if err != nil {
				return err
			}
			if taken {
				return ErrNameTaken
			}
		}

		tag, err := tx.Exec(ctx, `
			UPDATE items
			SET title = COALESCE($3, title), fields = jsonb_strip_nulls(fields || $4::jsonb)
			WHERE id = $2 AND room_id = $1
		`, roomID, itemID, title, string(patch))
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// Comments lists an item's comments, oldest first.
func Comments(ctx context.Context, roomID, itemID string) ([]Comment, error) {
	p, err := Pool()
	if err != nil {
		return nil, err
	}

	rows, err := p.Query(ctx, `
		SELECT id, item_id, member_id, body, created_at, edited_at
		FROM comments
		WHERE room_id = $1 AND item_id = $2
		ORDER BY created_at
	`, roomID, itemID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanComment)
}

// AddComment adds a comment. It returns ErrNotFound if the item isn't in the
// room.
func AddComment(ctx context.Context, roomID, itemID, memberID, body string) (Comment, error) {
	var c Comment
	err := execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO comments (room_id, item_id, member_id, body)
			SELECT $1, $2, $3, $4
			WHERE EXISTS (SELECT 1 FROM items WHERE id = $2 AND room_id = $1)
			RETURNING id, item_id, member_id, body, created_at, edited_at
		`, roomID, itemID, memberID, body)
		if err != nil {
			return err
		}
		c, err = pgx.CollectExactlyOneRow(rows, scanComment)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return c, err
}

// CommentAuthor returns the member who wrote a comment in the room.
func CommentAuthor(ctx context.Context, roomID, commentID string) (string, error) {
	p, err := Pool()
	if err != nil {
		return "", err
	}

	var memberID string
	err = p.QueryRow(ctx, `SELECT member_id FROM comments WHERE id = $2 AND room_id = $1`, roomID, commentID).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return memberID, err
}

// EditComment replaces a comment's body and marks it edited.
func EditComment(ctx context.Context, roomID, commentID, body string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE comments SET body = $3, edited_at = now()
			WHERE id = $2 AND room_id = $1
		`, roomID, commentID, body)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

// DeleteComment deletes a comment.
func DeleteComment(ctx context.Context, roomID, commentID string) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM comments WHERE id = $2 AND room_id = $1`, roomID, commentID)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

func scanComment(row pgx.CollectableRow) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.ItemID, &c.AuthorID, &c.Body, &c.CreatedAt, &c.EditedAt)
	return c, err
}

// execInRoom runs fn in a transaction and, if it succeeds, bumps the room's
// updated_at so polling clients pick up the change.
func execInRoom(ctx context.Context, roomID string, fn func(pgx.Tx) error) error {
	p, err := Pool()
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return touchRoom(ctx, tx, roomID)
	})
}
