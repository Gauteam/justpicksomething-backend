-- What a comment is: a plain comment, or a quick signal with optional text.
ALTER TABLE comments
    ADD COLUMN kind text NOT NULL DEFAULT 'comment'
        CHECK (kind IN ('comment', 'emphasis', 'up', 'down'));
