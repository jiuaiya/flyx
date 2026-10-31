package repo

import (
	"errors"
	"fmt"
	"go-backend/internal/store/model"
	"path/filepath"
	"testing"
)

func TestNodeTrafficBatchAcknowledgementIsIdempotent(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	initial := []*model.NodeMetric{{NodeID: 7, Timestamp: 1000, NetInBytes: 100, Uptime: 10}}
	batch := []*model.NodeMetric{{NodeID: 7, Timestamp: 2000, NetInBytes: 200, Uptime: 11}}
	if err := r.SaveNodeMetricBatch("stream", 1, initial); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := r.SaveNodeMetricBatch("stream", 2, batch); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := r.NodeTrafficSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if totals[7][0] != 100 {
		t.Fatalf("duplicate commit: %v", totals)
	}
	var count int64
	if err := r.DB().Model(&model.NodeMetric{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("duplicate raw metrics: %d", count)
	}
	batch[0].NetInBytes++
	if err := r.SaveNodeMetricBatch("stream", 2, batch); !errors.Is(err, ErrTrafficSequence) {
		t.Fatalf("reused identity accepted: %v", err)
	}
	if err := r.SaveNodeMetricBatch("stream", 4, batch); !errors.Is(err, ErrTrafficSequence) {
		t.Fatalf("sequence gap accepted: %v", err)
	}
}

func TestNodeTrafficUpgradeUsesLastSavedRawSample(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	old := []*model.NodeMetric{{NodeID: 1, Timestamp: 1000, Uptime: 10, NetInBytes: 1000}}
	if err := r.InsertNodeMetricBatch(old); err != nil {
		t.Fatal(err)
	}
	next := []*model.NodeMetric{{NodeID: 1, Timestamp: 2000, Uptime: 11, NetInBytes: 1200}}
	if err := r.SaveNodeMetricBatch("after-upgrade", 1, next); err != nil {
		t.Fatal(err)
	}
	totals, err := r.NodeTrafficSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if totals[1][0] != 200 {
		t.Fatalf("upgrade lost the saved baseline: %v", totals)
	}
}

func TestNodeTrafficPersistentBaselineRejectsStaleAndInterfaceResets(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "regression.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	samples := []*model.NodeMetric{
		{NodeID: 1, Timestamp: 100000, Uptime: 10000, NetInBytes: 54000000000, NetOutBytes: 51000000000},
		{NodeID: 1, Timestamp: 105000, Uptime: 9995, NetInBytes: 53999920000, NetOutBytes: 50999900000},
		{NodeID: 1, Timestamp: 110000, Uptime: 10010, NetInBytes: 54000000100, NetOutBytes: 51000000200},
		{NodeID: 1, Timestamp: 115000, Uptime: 10015, NetInBytes: 53000000000, NetOutBytes: 50000000000},
		{NodeID: 1, Timestamp: 120000, Uptime: 10020, NetInBytes: 53000000100, NetOutBytes: 50000000200},
		{NodeID: 1, Timestamp: 160000, Uptime: 30, NetInBytes: 1000, NetOutBytes: 2000},
	}
	for i, sample := range samples {
		// A fresh producer identity models a panel restart between reports.
		stream := fmt.Sprintf("restart-%d", i)
		for retry := 0; retry < 2; retry++ {
			if err := r.SaveNodeMetricBatch(stream, 1, []*model.NodeMetric{sample}); err != nil {
				t.Fatal(err)
			}
		}
	}
	totals, err := r.NodeTrafficSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if totals[1] != [2]int64{1200, 2400} {
		t.Fatalf("stale, reset, reboot or retry miscounted: %v", totals)
	}
}
