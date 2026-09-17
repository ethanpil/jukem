-- The card above the queue names the file an announcement played last, so
-- the play writes it down beside the play time.
ALTER TABLE announcements ADD COLUMN last_file TEXT;
