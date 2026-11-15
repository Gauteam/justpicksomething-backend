-- A comment can be about one cell (an option's value in one column) instead
-- of the whole option. Deleting the column deletes its cell comments.
ALTER TABLE comments ADD COLUMN column_id uuid REFERENCES columns (id) ON DELETE CASCADE;

CREATE INDEX comments_item_column ON comments (item_id, column_id) WHERE column_id IS NOT NULL;
