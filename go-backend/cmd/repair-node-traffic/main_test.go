package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRepairRetainedHoursDryRunApplyAndRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE node_metric(id INTEGER PRIMARY KEY, node_id INTEGER, timestamp INTEGER, uptime INTEGER, net_in_bytes INTEGER, net_out_bytes INTEGER)",
		"CREATE TABLE node_traffic(node_id INTEGER, hour INTEGER, in_bytes INTEGER, out_bytes INTEGER, PRIMARY KEY(node_id,hour))",
		"INSERT INTO node_metric VALUES(1,2,3590000,10000,54000000000,51000000000),(2,2,3605000,10015,54000000100,51000000200),(3,2,3605000,10010,53999920000,50999900000),(4,2,3610000,10020,54000000300,51000000500)",
		"INSERT INTO node_traffic VALUES(2,0,999,999),(2,3600000,54000000300,51000000500),(2,7200000,777,777)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	check := func(hour, wantIn, wantOut int64) {
		t.Helper()
		var in, out int64
		if err := db.QueryRow("SELECT in_bytes,out_bytes FROM node_traffic WHERE node_id=2 AND hour=?", hour).Scan(&in, &out); err != nil {
			t.Fatal(err)
		}
		if in != wantIn || out != wantOut {
			t.Fatalf("hour %d got (%d,%d), want (%d,%d)", hour, in, out, wantIn, wantOut)
		}
	}
	if err := repair(path, false); err != nil {
		t.Fatal(err)
	}
	check(3600000, 54000000300, 51000000500)
	if err := repair(path, true); err != nil {
		t.Fatal(err)
	}
	check(0, 999, 999)
	check(3600000, 300, 500)
	check(7200000, 777, 777)
	if err := repair(path, true); err != nil {
		t.Fatal(err)
	}
	check(3600000, 300, 500)
}
