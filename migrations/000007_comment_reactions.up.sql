-- Emphasis on comments: each member can mark a comment +1 (★, positive) or
-- -1 (⚑, a concern). One per member per comment.
CREATE TABLE comment_reactions (
    comment_id uuid NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    member_id  uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    value      smallint NOT NULL CHECK (value IN (-1, 1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, member_id)
);
