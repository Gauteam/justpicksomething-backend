ALTER TABLE rooms
    -- NULL means no limit.
    ADD COLUMN max_items integer CHECK (max_items > 0),
    ADD COLUMN vote_mode text NOT NULL DEFAULT 'thumbs' CHECK (vote_mode IN ('thumbs', 'budget')),
    -- Budget mode: how many votes each member can spread across options.
    ADD COLUMN votes_per_member integer NOT NULL DEFAULT 3 CHECK (votes_per_member > 0),
    -- Budget mode: whether a member can put more than one vote on an option.
    ADD COLUMN allow_duplicate_votes boolean NOT NULL DEFAULT false,
    -- Set while the room is locked in: options, votes, columns and comments
    -- are frozen.
    ADD COLUMN locked_at timestamptz;
