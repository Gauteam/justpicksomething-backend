CREATE TABLE rooms (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          text NOT NULL UNIQUE,
    name          text NOT NULL,
    passcode_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Bumped on every write in the room; clients poll with ?since= against it.
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id      uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    display_name text NOT NULL,
    -- SHA-256 of the member's bearer token.
    token_hash   bytea NOT NULL UNIQUE,
    is_admin     boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX members_room_display_name ON members (room_id, lower(display_name));

CREATE TABLE items (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    title      text NOT NULL,
    created_by uuid REFERENCES members (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX items_room_id ON items (room_id);

CREATE TABLE votes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    item_id    uuid NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    member_id  uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    -- Thumbs mode: +1 / -1. Budget mode (M3): number of votes placed.
    value      smallint NOT NULL CHECK (value <> 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (item_id, member_id)
);

CREATE INDEX votes_room_id ON votes (room_id);
