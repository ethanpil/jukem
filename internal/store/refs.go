package store

import (
	"context"
	"database/sql"
)

// Path comparisons in SQL use substr. SQLite measures its lengths in
// characters, so a non-ASCII folder name works. The comparisons are
// exact, so a folder that differs only by case is not touched. LIKE
// would be case-insensitive, and Go's len counts bytes.

// belowClause matches column = ? or column starting with ? + "/". The
// caller passes the path twice.
func belowClause(column string) string {
	return "(" + column + " = ? OR substr(" + column + ", 1, length(?) + 1) = ? || '/')"
}

// RepointDirectory updates rules, exceptions and announcements whose source
// is old or below it, after a move. It returns the number of rows changed.
func (s *Store) RepointDirectory(ctx context.Context, old, new string) (int64, error) {
	var total int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"schedules", "schedule_exceptions"} {
			res, err := tx.ExecContext(ctx, `UPDATE `+table+` SET source_ref = ? || substr(source_ref, length(?) + 1) WHERE source_type = 'directory' AND `+belowClause("source_ref"),
				new, old, old, old, old)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += n
		}
		// An announcement points at a folder or at one file. Both follow
		// the move, so the announcement still plays after it.
		res, err := tx.ExecContext(ctx, `UPDATE announcements SET source_ref = ? || substr(source_ref, length(?) + 1) WHERE `+belowClause("source_ref"),
			new, old, old, old, old)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		total += n
		return nil
	})
	return total, err
}

// DirectoryReferences returns the names of rules, exceptions and
// announcements whose source is path or below it.
func (s *Store) DirectoryReferences(ctx context.Context, path string) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT name FROM schedules WHERE source_type = 'directory' AND `+belowClause("source_ref")+
		` UNION ALL SELECT 'Exception ' || date FROM schedule_exceptions WHERE source_type = 'directory' AND `+belowClause("source_ref")+
		` UNION ALL SELECT 'Announcement ' || name FROM announcements WHERE `+belowClause("source_ref"),
		path, path, path, path, path, path, path, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// MoveDoNotPlay follows a moved file or folder.
func (s *Store) MoveDoNotPlay(ctx context.Context, old, new string, isDir bool) error {
	if !isDir {
		_, err := s.w.ExecContext(ctx, `UPDATE do_not_play SET file = ? WHERE file = ?`, new, old)
		return err
	}
	_, err := s.w.ExecContext(ctx, `UPDATE do_not_play SET file = ? || substr(file, length(?) + 1) WHERE substr(file, 1, length(?) + 1) = ? || '/'`, new, old, old, old)
	return err
}

// DeleteDoNotPlayUnder removes entries for a deleted file or folder.
func (s *Store) DeleteDoNotPlayUnder(ctx context.Context, path string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM do_not_play WHERE `+belowClause("file"), path, path, path)
	return err
}
