CREATE TABLE columns (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    name       text NOT NULL,
    type       text NOT NULL CHECK (type IN ('text', 'boolean', 'textarea')),
    position   integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX columns_room_name ON columns (room_id, lower(name));

-- Column values keyed by column ID: string for text/textarea, boolean for
-- boolean. Not named "values": that's a reserved word.
ALTER TABLE items ADD COLUMN fields jsonb NOT NULL DEFAULT '{}';

-- Manual (drag) order within the room. Existing items keep creation order.
ALTER TABLE items ADD COLUMN position integer;
UPDATE items
SET position = ordered.n
FROM (
    SELECT id, row_number() OVER (PARTITION BY room_id ORDER BY created_at) AS n
    FROM items
) AS ordered
WHERE items.id = ordered.id;
ALTER TABLE items ALTER COLUMN position SET NOT NULL;

CREATE TABLE comments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    item_id    uuid NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    member_id  uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    edited_at  timestamptz
);

CREATE INDEX comments_item_created ON comments (item_id, created_at);
