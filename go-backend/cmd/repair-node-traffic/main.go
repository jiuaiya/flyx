// repair-node-traffic replays retained raw samples using the same accounting
// rules as the running panel. Stop the backend and back up SQLite before -apply.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "github.com/glebarez/go-sqlite"

	"go-backend/internal/metrics"
	"go-backend/internal/store/model"
)

const hourMS = int64(3600000)

type change struct {
	NodeID int64    `json:"nodeId"`
	Hour   int64    `json:"hour"`
	Before [2]int64 `json:"before"`
	After  [2]int64 `json:"after"`
}

func main() {
	path := flag.String("db", "", "SQLite database path")
	apply := flag.Bool("apply", false, "write corrections; default is a dry run")
	flag.Parse()
	if err := repair(*path, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func repair(path string, apply bool) error {
	if path == "" {
		return fmt.Errorf("-db is required")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT node_id, hour, in_bytes, out_bytes FROM node_traffic ORDER BY node_id, hour")
	if err != nil {
		return err
	}
	old := map[int64]map[int64][2]int64{}
	for rows.Next() {
		var node, hour, in, out int64
		if err := rows.Scan(&node, &hour, &in, &out); err != nil {
			rows.Close()
			return err
		}
		if old[node] == nil {
			old[node] = map[int64][2]int64{}
		}
		old[node][hour] = [2]int64{in, out}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	changes := []change{}
	skipped := 0
	for node, hours := range old {
		rows, err := tx.Query("SELECT id, timestamp, uptime, net_in_bytes, net_out_bytes FROM node_metric WHERE node_id=? ORDER BY timestamp, id", node)
		if err != nil {
			return err
		}
		var prev *model.NodeMetric
		first := int64(-1)
		rebuilt := map[int64][2]int64{}
		for rows.Next() {
			cur := &model.NodeMetric{}
			if err := rows.Scan(&cur.ID, &cur.Timestamp, &cur.Uptime, &cur.NetInBytes, &cur.NetOutBytes); err != nil {
				rows.Close()
				return err
			}
			if first < 0 {
				first = cur.Timestamp
			}
			in, out, accepted := metrics.NodeTrafficDelta(prev, cur)
			if !accepted {
				continue
			}
			if prev != nil {
				hour := cur.Timestamp - cur.Timestamp%hourMS
				v := rebuilt[hour]
				v[0] += in
				v[1] += out
				rebuilt[hour] = v
			}
			prev = cur
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		// The first retained hour may have pruned samples. Leave it intact.
		completeFrom := (first/hourMS + 1) * hourMS
		for hour, before := range hours {
			after, observed := rebuilt[hour]
			if first < 0 || hour < completeFrom || !observed {
				skipped++
				continue
			}
			if before == after {
				continue
			}
			changes = append(changes, change{NodeID: node, Hour: hour, Before: before, After: after})
		}
	}
	if apply {
		for _, c := range changes {
			r, err := tx.Exec("UPDATE node_traffic SET in_bytes=?, out_bytes=? WHERE node_id=? AND hour=? AND in_bytes=? AND out_bytes=?", c.After[0], c.After[1], c.NodeID, c.Hour, c.Before[0], c.Before[1])
			if err != nil {
				return err
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				return fmt.Errorf("traffic changed concurrently for node %d hour %d", c.NodeID, c.Hour)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Applied      bool     `json:"applied"`
		SkippedHours int      `json:"skippedHours"`
		Changes      []change `json:"changes"`
	}{apply, skipped, changes})
}
