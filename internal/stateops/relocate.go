package stateops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// relocateDatabase rewrites operational paths when a backup is inspected in
// another home. Repository source paths remain unchanged.
func relocateDatabase(ctx context.Context, path, from, to string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range []struct{ table, column string }{
		{"sessions", "workspace"},
		{"checkpoints", "archive_path"},
		{"invocations", "log_path"},
	} {
		query := fmt.Sprintf("UPDATE %s SET %s = ? || substr(%s, ? + 1) WHERE substr(%s, 1, ?) = ?", item.table, item.column, item.column, item.column)
		if _, err := tx.ExecContext(ctx, query, to, len(from), len(from), from); err != nil {
			return err
		}
	}
	for _, item := range []struct{ table, column, id string }{
		{"candidates", "checks", "id"},
		{"events", "payload", "sequence"},
	} {
		query := fmt.Sprintf("SELECT %s, %s FROM %s", item.id, item.column, item.table)
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		type update struct {
			id    any
			value string
		}
		var changes []update
		for rows.Next() {
			var id any
			var value string
			if err := rows.Scan(&id, &value); err != nil {
				return errors.Join(err, rows.Close())
			}
			changed := strings.ReplaceAll(value, from, to)
			if changed != value {
				changes = append(changes, update{id, changed})
			}
		}
		if err := rows.Err(); err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, change := range changes {
			query := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", item.table, item.column, item.id)
			if _, err := tx.ExecContext(ctx, query, change.value, change.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
