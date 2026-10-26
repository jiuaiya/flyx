package repo

import (
	"errors"

	"go-backend/internal/store/model"
	"gorm.io/gorm/clause"
)

func (r *Repository) SaveNodeRouteSnapshot(nodeID int64, data string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"data"}),
	}).Create(&model.NodeRouteSnapshot{NodeID: nodeID, Data: data}).Error
}

func (r *Repository) ListNodeRouteSnapshots() ([]model.NodeRouteSnapshot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var snapshots []model.NodeRouteSnapshot
	err := r.db.Model(&model.NodeRouteSnapshot{}).Select("node_route_snapshot.*").
		Joins("JOIN node ON node.id = node_route_snapshot.node_id").
		Where("node.is_remote = ?", 0).Find(&snapshots).Error
	return snapshots, err
}
