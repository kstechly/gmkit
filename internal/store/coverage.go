package store

import (
	"context"
	"encoding/json"
	"github.com/johnlindquist/gmkit/internal/coverage"
)

func (s *Store) SaveFolderScan(ctx context.Context, report coverage.Folder, ids []string) error {
	b, e := json.Marshal(report)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO folder_scans(folder,report) VALUES(?,?) ON CONFLICT(folder) DO UPDATE SET report=excluded.report", report.Folder, string(b)); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM folder_members WHERE folder=?", report.Folder); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = tx.ExecContext(ctx, "INSERT INTO folder_members VALUES(?,?)", report.Folder, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) FolderScans(ctx context.Context) ([]coverage.Folder, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT report FROM folder_scans ORDER BY folder")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []coverage.Folder{}
	for rows.Next() {
		var raw string
		var r coverage.Folder
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) CachedCoverage(ctx context.Context) (coverage.Read, error) {
	scans, e := s.FolderScans(ctx)
	return coverage.Read{Source: "cached", Folder: "all_known", FolderScans: scans, MessageHistory: coverage.Messages{Scope: "local_cache", StopReason: "history_coverage_unknown"}}, e
}
