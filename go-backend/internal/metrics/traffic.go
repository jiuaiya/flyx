package metrics

import (
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// NodeTrafficDelta keeps live collection and offline historical repairs consistent.
func NodeTrafficDelta(prev, cur *model.NodeMetric) (in, out int64, accepted bool) {
	return repo.NodeTrafficDelta(prev, cur)
}
