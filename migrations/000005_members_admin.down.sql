ALTER TABLE rooms
    DROP COLUMN IF EXISTS passcode_changed_at,
    DROP COLUMN IF EXISTS everyone_admin;
