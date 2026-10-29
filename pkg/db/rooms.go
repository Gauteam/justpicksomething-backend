package db

import (
	"context"
	"encoding/json"
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
	// Settings. MaxItems nil means no limit.
	MaxItems            *int       `json:"maxItems"`
	VoteMode            string     `json:"voteMode"`
	VotesPerMember      int        `json:"votesPerMember"`
	AllowDuplicateVotes bool       `json:"allowDuplicateVotes"`
	LockedAt            *time.Time `json:"lockedAt"`
}

// NewItem is an item to add, with optional column values.
type NewItem struct {
	Title  string
	Fields map[string]any
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
		SELECT
			r.id, r.slug, r.name, r.updated_at,
			r.max_items, r.vote_mode, r.votes_per_member, r.allow_duplicate_votes, r.locked_at,
			m.id, m.display_name, m.is_admin
		FROM members m
		JOIN rooms r ON r.id = m.room_id
		WHERE m.token_hash = $1 AND r.slug = $2
	`, tokenHash, slug).Scan(
		&s.Room.ID, &s.Room.Slug, &s.Room.Name, &s.Room.UpdatedAt,
		&s.Room.MaxItems, &s.Room.VoteMode, &s.Room.VotesPerMember, &s.Room.AllowDuplicateVotes, &s.Room.LockedAt,
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
// already in the room (ignoring case). It returns the items actually added,
// or ErrLimit (adding nothing) if they would take the room past maxItems.
func AddItems(ctx context.Context, roomID, memberID string, newItems []NewItem, maxItems *int) ([]Item, error) {
	titles := make([]string, len(newItems))
	fields := make([]string, len(newItems))
	for i, it := range newItems {
		titles[i] = it.Title
		f := it.Fields
		if f == nil {
			f = map[string]any{}
		}
		b, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		fields[i] = string(b)
	}

	var items []Item
	err := execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		// Lock the room row so concurrent adds can't both slip under the limit.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM rooms WHERE id = $1 FOR UPDATE`, roomID); err != nil {
			return err
		}

		// Offset created_at by position so the batch keeps its order when
		// items tie on score.
		rows, err := tx.Query(ctx, `
			INSERT INTO items (room_id, title, fields, created_by, created_at, position)
			SELECT
				$1, t.title, jsonb_strip_nulls(t.fields::jsonb), $2,
				now() + t.ord * interval '1 microsecond',
				(SELECT COALESCE(max(position), 0) FROM items WHERE room_id = $1) + t.ord
			FROM unnest($3::text[], $4::text[]) WITH ORDINALITY AS t(title, fields, ord)
			WHERE NOT EXISTS (
				SELECT 1 FROM items i WHERE i.room_id = $1 AND lower(i.title) = lower(t.title)
			)
			RETURNING id, title, created_by, created_at, fields, position
		`, roomID, memberID, titles, fields)
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Item, error) {
			var it Item
			err := row.Scan(&it.ID, &it.Title, &it.CreatedBy, &it.CreatedAt, &it.Fields, &it.Position)
			return it, err
		})
		if err != nil {
			return err
		}

		if maxItems != nil {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM items WHERE room_id = $1`, roomID).Scan(&count); err != nil {
				return err
			}
			if count > *maxItems {
				return ErrLimit
			}
		}
		return nil
	})
	if items == nil {
		items = []Item{}
	}
	return items, err
}

// SetVote sets memberID's vote on an item: ±1 in thumbs mode, or the number
// of votes placed in budget mode. With a non-nil budget, it returns ErrLimit if
// the member's votes across the room would exceed it. It returns ErrNotFound
// if the item isn't in the room.
func SetVote(ctx context.Context, roomID, itemID, memberID string, value int, budget *int) error {
	return execInRoom(ctx, roomID, func(tx pgx.Tx) error {
		if budget != nil {
			// Lock the member row so parallel votes can't overspend.
			var used int
			err := tx.QueryRow(ctx, `
				SELECT COALESCE((
					SELECT sum(value) FROM votes
					WHERE room_id = $1 AND member_id = $2 AND item_id <> $3
				), 0)
				FROM members WHERE id = $2
				FOR UPDATE
			`, roomID, memberID, itemID).Scan(&used)
			if err != nil {
				return err
			}
			if used+value > *budget {
				return ErrLimit
			}
		}

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
		return nil
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
