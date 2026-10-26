package repo

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestNodeRouteSnapshotUpsertAndCascade(t *testing.T) {
	r, err := Open(t.TempDir() + "/routes.db")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	node := model.Node{Name: "test", Secret: "test", ServerIP: "192.0.2.1"}
	if err := r.db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"route":"old"}`, `{"route":"new"}`} {
		if err := r.SaveNodeRouteSnapshot(node.ID, data); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.ListNodeRouteSnapshots()
	if err != nil || len(rows) != 1 || rows[0].Data != `{"route":"new"}` {
		t.Fatalf("upsert: %+v, %v", rows, err)
	}
	if err := r.DeleteNodeCascade(node.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := r.db.Model(&model.NodeRouteSnapshot{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphaned snapshots: %d, %v", count, err)
	}
}
