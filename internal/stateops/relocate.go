package stateops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/fault"
	_ "modernc.org/sqlite"
)

// relocateDatabase rewrites owned runtime paths in one transaction. Repository
// sources, task text and URLs are preserved, even when they mention the old root.
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
	ownedPath := func(value string) (string, error) {
		if value == "" {
			return value, nil
		}
		changed, owned := relocatePath(value, from, to)
		if !owned {
			return "", fault.New(fault.CodeSessionCorrupt, "runtime path is outside the backup state root")
		}
		return changed, nil
	}
	for _, item := range []struct{ table, column string }{
		{"sessions", "workspace"}, {"checkpoints", "archive_path"}, {"invocations", "log_path"},
	} {
		if err := rewriteColumn(ctx, tx, item.table, item.column, "id", ownedPath); err != nil {
			return err
		}
	}
	for _, item := range []struct{ table, column, id string }{
		{"candidates", "checks", "id"}, {"candidates", "deliverables", "id"}, {"events", "payload", "sequence"},
	} {
		if err := rewriteColumn(ctx, tx, item.table, item.column, item.id, func(value string) (string, error) {
			return relocateJSON(value, from, to)
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func rewriteColumn(ctx context.Context, tx *sql.Tx, table, column, idColumn string, rewrite func(string) (string, error)) error {
	// Identifiers are fixed above; only data values are supplied as parameters.
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT %s, %s FROM %s", idColumn, column, table))
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
		var value sql.NullString
		if err := rows.Scan(&id, &value); err != nil {
			return errors.Join(err, rows.Close())
		}
		if !value.Valid {
			continue
		}
		changed, err := rewrite(value.String)
		if err != nil {
			return errors.Join(fault.At(err, "relocate "+table+"."+column), rows.Close())
		}
		if changed != value.String {
			changes = append(changes, update{id, changed})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, change := range changes {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", table, column, idColumn), change.value, change.id); err != nil {
			return err
		}
	}
	return nil
}

func relocatePath(path, from, to string) (string, bool) {
	if !filepath.IsAbs(path) {
		return path, false
	}
	rel, err := filepath.Rel(from, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path, false
	}
	return filepath.Join(to, rel), true
}

func relocateJSON(value, from, to string) (string, error) {
	var document any
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.UseNumber() // Audit events may contain counters larger than 2^53.
	if err := decoder.Decode(&document); err != nil {
		return "", fault.Wrap(fault.CodeSessionCorrupt, "decode relocation record", err, "state JSON is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fault.New(fault.CodeSessionCorrupt, "state JSON contains trailing data")
	}
	if !relocateJSONPaths(document, from, to) {
		return value, nil
	}
	data, err := json.Marshal(document)
	if err != nil {
		return "", fault.Wrap(fault.CodeSessionCorrupt, "encode relocation record", err, "state JSON cannot be encoded")
	}
	return string(data), nil
}

func relocateJSONPaths(document any, from, to string) bool {
	changed := false
	switch node := document.(type) {
	case map[string]any:
		for key, value := range node {
			// These are the owned path fields in check and artifact results.
			if key == "log_path" || key == "local_path" {
				path, ok := value.(string)
				if !ok {
					continue
				}
				if moved, owned := relocatePath(path, from, to); owned && moved != path {
					node[key], changed = moved, true
				}
				continue
			}
			changed = relocateJSONPaths(value, from, to) || changed
		}
	case []any:
		for _, value := range node {
			changed = relocateJSONPaths(value, from, to) || changed
		}
	}
	return changed
}
