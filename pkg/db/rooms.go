package db

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrSlugTaken = errors.New("slug taken")
	ErrNameTaken = errors.New("display name taken")
)

type Room struct {
	ID        string    `json:"-"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Member struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	IsAdmin     bool   `json:"isAdmin"`
}

type Item struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedBy *string   `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	// Column values keyed by column ID.
	Fields       map[string]any `json:"fields"`
	Position     int            `json:"position"`
	CommentCount int            `json:"commentCount"`
	Up           int            `json:"up"`
	Down         int            `json:"down"`
	Score        int            `json:"score"`
	MyVote       int            `json:"myVote"`
}

// Session is an authenticated member and the room they belong to.
type Session struct {
	Room   Room
	Member Member
}

// CreateRoom inserts a room and its first (admin) member.
func CreateRoom(ctx context.Context, slug, name, passcodeHash, displayName string, tokenHash []byte) (Session, error) {
	p, err := Pool()
	if err != nil {
		return Session{}, err
	}

	var s Session
	err = pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO rooms (slug, name, passcode_hash)
			VALUES ($1, $2, $3)
			RETURNING id, slug, name, updated_at
		`, slug, name, passcodeHash).Scan(&s.Room.ID, &s.Room.Slug, &s.Room.Name, &s.Room.UpdatedAt)
		if err != nil {
			if isUniqueViolation(err, "rooms_slug_key") {
				return ErrSlugTaken
			}
			return err
		}

		s.Member, err = insertMember(ctx, tx, s.Room.ID, displayName, tokenHash, true)
		return err
	})
	return s, err
}

// PasscodeHash returns the room's ID and stored passcode hash.
func PasscodeHash(ctx context.Context, slug string) (roomID, hash string, err error) {
	p, err := Pool()
	if err != nil {
		return "", "", err
	}

	err = p.QueryRow(ctx, `SELECT id, passcode_hash FROM rooms WHERE slug = $1`, slug).Scan(&roomID, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return roomID, hash, err
}

// AddMember adds a non-admin member to a room.
func AddMember(ctx context.Context, roomID, displayName string, tokenHash []byte) (Member, error) {
	p, err := Pool()
	if err != nil {
		return Member{}, err
	}

	var m Member
	err = pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		var err error
		if m, err = insertMember(ctx, tx, roomID, displayName, tokenHash, false); err != nil {
			return err
		}
		return touchRoom(ctx, tx, roomID)
	})
	return m, err
}

// SessionByToken finds the member holding tokenHash in the room with slug.
func SessionByToken(ctx context.Context, slug string, tokenHash []byte) (Session, error) {
	p, err := Pool()
	if err != nil {
		return Session{}, err
	}

	var s Session
	err = p.QueryRow(ctx, `
		SELECT r.id, r.slug, r.name, r.updated_at, m.id, m.display_name, m.is_admin
		FROM members m
		JOIN rooms r ON r.id = m.room_id
		WHERE m.token_hash = $1 AND r.slug = $2
	`, tokenHash, slug).Scan(
		&s.Room.ID, &s.Room.Slug, &s.Room.Name, &s.Room.UpdatedAt,
		&s.Member.ID, &s.Member.DisplayName, &s.Member.IsAdmin,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

// Members lists a room's members, oldest first.
func Members(ctx context.Context, roomID string) ([]Member, error) {
	p, err := Pool()
	if err != nil {
		return nil, err
	}

	rows, err := p.Query(ctx, `
		SELECT id, display_name, is_admin
		FROM members
		WHERE room_id = $1
		ORDER BY created_at
	`, roomID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var m Member
		err := row.Scan(&m.ID, &m.DisplayName, &m.IsAdmin)
		return m, err
	})
}

// Items lists a room's items with vote tallies, comment counts and memberID's
// own vote, highest score first, then oldest first.
func Items(ctx context.Context, roomID, memberID string) ([]Item, error) {
	p, err := Pool()
	if err != nil {
		return nil, err
	}

	rows, err := p.Query(ctx, `
		SELECT
			i.id,
			i.title,
			i.created_by,
			i.created_at,
			i.fields,
			i.position,
			(SELECT count(*) FROM comments c WHERE c.item_id = i.id),
			COALESCE(SUM(v.value) FILTER (WHERE v.value > 0), 0),
			COALESCE(-SUM(v.value) FILTER (WHERE v.value < 0), 0),
			COALESCE(MAX(v.value) FILTER (WHERE v.member_id = $2), 0)
		FROM items i
		LEFT JOIN votes v ON v.item_id = i.id
		WHERE i.room_id = $1
		GROUP BY i.id
	`, roomID, memberID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
		var it Item
		err := row.Scan(
			&it.ID, &it.Title, &it.CreatedBy, &it.CreatedAt, &it.Fields, &it.Position, &it.CommentCount,
			&it.Up, &it.Down, &it.MyVote,
		)
		it.Score = it.Up - it.Down
		return it, err
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Score != items[b].Score {
			return items[a].Score > items[b].Score
		}
		return items[a].CreatedAt.Before(items[b].CreatedAt)
	})
	return items, nil
}

// AddItems appends items to a room in the given order, skipping titles
// already in the room (ignoring case). It returns the items actually added.
func AddItems(ctx context.Context, roomID, memberID string, titles []string) ([]Item, error) {
	p, err := Pool()
	if err != nil {
		return nil, err
	}

	var items []Item
	err = pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		// Offset created_at by position so the batch keeps its order when
		// items tie on score.
		rows, err := tx.Query(ctx, `
			INSERT INTO items (room_id, title, created_by, created_at, position)
			SELECT
				$1, t.title, $2,
				now() + t.ord * interval '1 microsecond',
				(SELECT COALESCE(max(position), 0) FROM items WHERE room_id = $1) + t.ord
			FROM unnest($3::text[]) WITH ORDINALITY AS t(title, ord)
			WHERE NOT EXISTS (
				SELECT 1 FROM items i WHERE i.room_id = $1 AND lower(i.title) = lower(t.title)
			)
			RETURNING id, title, created_by, created_at, fields, position
		`, roomID, memberID, titles)
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
			var it Item
			err := row.Scan(&it.ID, &it.Title, &it.CreatedBy, &it.CreatedAt, &it.Fields, &it.Position)
			return it, err
		})
		if err != nil || len(items) == 0 {
			return err
		}
		return touchRoom(ctx, tx, roomID)
	})
	if items == nil {
		items = []Item{}
	}
	return items, err
}

// SetVote sets memberID's vote on an item. It returns ErrNotFound if the item
// isn't in the room.
func SetVote(ctx context.Context, roomID, itemID, memberID string, value int) error {
	p, err := Pool()
	if err != nil {
		return err
	}

	return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO votes (room_id, item_id, member_id, value)
			SELECT $1, $2, $3, $4
			WHERE EXISTS (SELECT 1 FROM items WHERE id = $2 AND room_id = $1)
			ON CONFLICT (item_id, member_id)
			DO UPDATE SET value = EXCLUDED.value, updated_at = now()
		`, roomID, itemID, memberID, value)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return touchRoom(ctx, tx, roomID)
	})
}

// ClearVote removes memberID's vote on an item, if any.
func ClearVote(ctx context.Context, roomID, itemID, memberID string) error {
	p, err := Pool()
	if err != nil {
		return err
	}

	return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM votes
			WHERE room_id = $1 AND item_id = $2 AND member_id = $3
		`, roomID, itemID, memberID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return touchRoom(ctx, tx, roomID)
	})
}

func insertMember(ctx context.Context, tx pgx.Tx, roomID, displayName string, tokenHash []byte, isAdmin bool) (Member, error) {
	var m Member
	err := tx.QueryRow(ctx, `
		INSERT INTO members (room_id, display_name, token_hash, is_admin)
		VALUES ($1, $2, $3, $4)
		RETURNING id, display_name, is_admin
	`, roomID, displayName, tokenHash, isAdmin).Scan(&m.ID, &m.DisplayName, &m.IsAdmin)
	if isUniqueViolation(err, "members_room_display_name") {
		return Member{}, ErrNameTaken
	}
	return m, err
}

// touchRoom bumps the room's updated_at so polling clients see a change.
func touchRoom(ctx context.Context, tx pgx.Tx, roomID string) error {
	_, err := tx.Exec(ctx, `UPDATE rooms SET updated_at = clock_timestamp() WHERE id = $1`, roomID)
	return err
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
