package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "jukem.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAlertsOnePerKind(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	a, created, err := st.RaiseAlert(ctx, "dead_air", "first", "fix")
	if err != nil || !created || a.Count != 1 {
		t.Fatalf("first raise: %+v created=%v err=%v", a, created, err)
	}
	a, created, err = st.RaiseAlert(ctx, "dead_air", "second", "fix")
	if err != nil || created || a.Count != 2 || a.Message != "second" {
		t.Fatalf("repeat raise: %+v created=%v err=%v", a, created, err)
	}
	list, err := st.ActiveAlerts(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("active: %v %v", list, err)
	}
	if ok, err := st.ResolveAlert(ctx, "dead_air"); err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	if ok, _ := st.ResolveAlert(ctx, "dead_air"); ok {
		t.Fatal("resolved twice")
	}
	if _, created, _ := st.RaiseAlert(ctx, "dead_air", "again", ""); !created {
		t.Fatal("a resolved alert must start a new row")
	}
	list, _ = st.ActiveAlerts(ctx)
	if ok, err := st.DismissAlert(ctx, list[0].ID); err != nil || !ok {
		t.Fatalf("dismiss: %v %v", ok, err)
	}
	if err := st.TrimAlerts(ctx, 0); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryTrim(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := st.AddHistory(ctx, HistoryRow{StartedAt: base.Add(time.Duration(i) * time.Minute), File: "a.mp3", Source: "Morning"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.ListHistory(ctx, 0, 10)
	if err != nil || len(all) != 5 {
		t.Fatalf("list: %v %v", all, err)
	}
	rows, err := st.ListHistory(ctx, all[1].ID, 10)
	if err != nil || len(rows) != 3 || !rows[0].StartedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("paging: %v %v", rows, err)
	}
	last, ok, err := st.LastHistoryAt(ctx)
	if err != nil || !ok || !last.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("last: %v %v %v", last, ok, err)
	}
	// Rows older than any retention are gone; the row limit keeps 2.
	if err := st.TrimHistory(ctx, 100000, 2); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.ListHistory(ctx, 0, 10)
	if len(rows) != 2 || !rows[0].StartedAt.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("after trim: %v", rows)
	}
	if err := st.TrimHistory(ctx, 1, 100); err != nil {
		t.Fatal(err)
	}
	if rows, _ = st.ListHistory(ctx, 0, 10); len(rows) != 0 {
		t.Fatalf("age trim kept %v", rows)
	}
}
