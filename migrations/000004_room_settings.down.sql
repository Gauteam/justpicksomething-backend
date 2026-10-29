ALTER TABLE rooms
    DROP COLUMN IF EXISTS locked_at,
    DROP COLUMN IF EXISTS allow_duplicate_votes,
    DROP COLUMN IF EXISTS votes_per_member,
    DROP COLUMN IF EXISTS vote_mode,
    DROP COLUMN IF EXISTS max_items;
