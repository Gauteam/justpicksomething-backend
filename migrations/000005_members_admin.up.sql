ALTER TABLE rooms
    -- When true, every member can do admin things, including later joiners.
    ADD COLUMN everyone_admin boolean NOT NULL DEFAULT false,
    -- Lets members' devices tell their saved passcode is out of date.
    ADD COLUMN passcode_changed_at timestamptz NOT NULL DEFAULT now();
