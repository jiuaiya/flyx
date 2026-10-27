package metrics

import (
	"context"
	"log"
	"sync"
	"time"

	"go-backend/internal/monitoring"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type SystemInfo struct {
	Uptime           uint64  `json:"uptime"`
	BytesReceived    uint64  `json:"bytes_received"`
	BytesTransmitted uint64  `json:"bytes_transmitted"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemoryUsage      float64 `json:"memory_usage"`
	DiskUsage        float64 `json:"disk_usage"`
	Load1            float64 `json:"load1"`
	Load5            float64 `json:"load5"`
	Load15           float64 `json:"load15"`
	TCPConns         int64   `json:"tcp_conns"`
	UDPConns         int64   `json:"udp_conns"`
	NetInSpeed       int64   `json:"net_in_speed"`
	NetOutSpeed      int64   `json:"net_out_speed"`
}

// NodeTrafficRetention keeps a little over a year of hourly node traffic.
const NodeTrafficRetention = 400 * 24 * time.Hour

type trafficKey struct {
	node, hour int64
}

type IngestionService struct {
	repo          *repo.Repository
	nodeBuffer    []*model.NodeMetric
	nodeBufferMu  sync.Mutex
	flushInterval time.Duration

	// Keep uptime and receipt time with the counters so delayed reports and
	// disappearing virtual interfaces cannot look like a machine reboot.
	lastMetric map[int64]*model.NodeMetric
	traffic    map[trafficKey][2]int64
}

func NewIngestionService(repo *repo.Repository) *IngestionService {
	return &IngestionService{
		repo:          repo,
		nodeBuffer:    make([]*model.NodeMetric, 0, 500),
		flushInterval: 30 * time.Second,
		lastMetric:    map[int64]*model.NodeMetric{},
		traffic:       map[trafficKey][2]int64{},
	}
}

// A counter can fall when an interface disappears or the agent changes which
// interfaces it reports. Only a confirmed reboot may count the new value in full.
func counterDelta(prev, cur int64) int64 {
	if cur >= prev {
		return cur - prev
	}
	return 0
}

// NodeTrafficDelta also serves offline repairs of retained raw metrics. A lower
// uptime is a reboot only if the new uptime fits in the time since the previous
// report (plus five seconds of sampling/clock tolerance). Otherwise it is stale.
func NodeTrafficDelta(prev, cur *model.NodeMetric) (in, out int64, accepted bool) {
	if cur.NetInBytes < 0 || cur.NetOutBytes < 0 {
		return 0, 0, false
	}
	if prev == nil {
		return 0, 0, true
	}
	if prev.Uptime > 0 && cur.Uptime == 0 {
		return 0, 0, false
	}
	if prev.Uptime > 0 && cur.Uptime > 0 && cur.Uptime < prev.Uptime {
		elapsed := (cur.Timestamp - prev.Timestamp) / 1000
		if elapsed < 0 || cur.Uptime > elapsed+5 {
			return 0, 0, false
		}
		return cur.NetInBytes, cur.NetOutBytes, true
	}
	if cur.Uptime > 0 && cur.Uptime == prev.Uptime &&
		(cur.NetInBytes < prev.NetInBytes || cur.NetOutBytes < prev.NetOutBytes) {
		return 0, 0, false
	}
	return counterDelta(prev.NetInBytes, cur.NetInBytes), counterDelta(prev.NetOutBytes, cur.NetOutBytes), true
}

func (s *IngestionService) Start(ctx context.Context) {
	flushTicker := time.NewTicker(s.flushInterval)
	defer flushTicker.Stop()

	pruneTicker := time.NewTicker(1 * time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushNodeMetrics()
			return
		case <-flushTicker.C:
			s.flushNodeMetrics()
		case <-pruneTicker.C:
			s.pruneMetrics()
		}
	}
}

func (s *IngestionService) RecordNodeMetric(nodeID int64, info SystemInfo) {
	m := &model.NodeMetric{
		NodeID:      nodeID,
		Timestamp:   time.Now().UnixMilli(),
		CPUUsage:    info.CPUUsage,
		MemUsage:    info.MemoryUsage,
		DiskUsage:   info.DiskUsage,
		NetInBytes:  int64(info.BytesReceived),
		NetOutBytes: int64(info.BytesTransmitted),
		NetInSpeed:  info.NetInSpeed,
		NetOutSpeed: info.NetOutSpeed,
		Load1:       info.Load1,
		Load5:       info.Load5,
		Load15:      info.Load15,
		TCPConns:    info.TCPConns,
		UDPConns:    info.UDPConns,
		Uptime:      int64(info.Uptime),
	}

	s.nodeBufferMu.Lock()
	in, out, accepted := NodeTrafficDelta(s.lastMetric[nodeID], m)
	if !accepted {
		s.nodeBufferMu.Unlock()
		return
	}
	s.nodeBuffer = append(s.nodeBuffer, m)
	shouldFlush := len(s.nodeBuffer) >= 200
	// The first report after a panel restart only sets the baseline.
	if s.lastMetric[nodeID] != nil {
		k := trafficKey{nodeID, m.Timestamp - m.Timestamp%int64(time.Hour/time.Millisecond)}
		t := s.traffic[k]
		t[0] += in
		t[1] += out
		s.traffic[k] = t
	}
	s.lastMetric[nodeID] = m
	s.nodeBufferMu.Unlock()

	if shouldFlush {
		go s.flushNodeMetrics()
	}
}

func (s *IngestionService) flushNodeMetrics() {
	s.nodeBufferMu.Lock()
	if len(s.nodeBuffer) == 0 {
		s.nodeBufferMu.Unlock()
		return
	}
	buffer := s.nodeBuffer
	s.nodeBuffer = make([]*model.NodeMetric, 0, 500)
	traffic := s.traffic
	s.traffic = map[trafficKey][2]int64{}
	s.nodeBufferMu.Unlock()

	if s.repo == nil {
		return
	}
	if err := s.repo.InsertNodeMetricBatch(buffer); err != nil {
		log.Printf("monitoring write failed op=node_metric.flush count=%d err=%v", len(buffer), err)
	}
	rows := make([]model.NodeTraffic, 0, len(traffic))
	for k, v := range traffic {
		// Keep observed idle hours too, so charts distinguish zero from no report.
		rows = append(rows, model.NodeTraffic{NodeID: k.node, Hour: k.hour, InBytes: v[0], OutBytes: v[1]})
	}
	if err := s.repo.AddNodeTraffic(rows); err != nil {
		log.Printf("monitoring write failed op=node_traffic.flush count=%d err=%v", len(rows), err)
	}
}

func (s *IngestionService) pruneMetrics() {
	s.pruneMetricsAt(time.Now())
}

func (s *IngestionService) retentionDaysFromConfig() int {
	if s == nil || s.repo == nil {
		return monitoring.DefaultMonitorRetentionDays
	}
	cfg, err := s.repo.GetConfigsByNames([]string{monitoring.ConfigMonitorRetentionDays})
	if err != nil {
		return monitoring.DefaultMonitorRetentionDays
	}
	return monitoring.MonitoringRetentionDaysFromConfigMap(cfg)
}

func (s *IngestionService) pruneMetricsAt(now time.Time) {
	cutoff := now.Add(-time.Duration(s.retentionDaysFromConfig()) * 24 * time.Hour).UnixMilli()
	if s.repo == nil {
		return
	}
	if err := s.repo.PruneNodeMetrics(cutoff); err != nil {
		log.Printf("monitoring prune failed op=node_metric cutoff=%d err=%v", cutoff, err)
	}
	if err := s.repo.PruneTunnelMetrics(cutoff); err != nil {
		log.Printf("monitoring prune failed op=tunnel_metric cutoff=%d err=%v", cutoff, err)
	}
	if err := s.repo.PruneServiceMonitorResults(cutoff); err != nil {
		log.Printf("monitoring prune failed op=service_monitor_result cutoff=%d err=%v", cutoff, err)
	}
	if err := s.repo.PruneNodeLatencySamples(cutoff); err != nil {
		log.Printf("monitoring prune failed op=node_latency_sample cutoff=%d err=%v", cutoff, err)
	}
	if err := s.repo.PruneNodeTraffic(now.Add(-NodeTrafficRetention).UnixMilli()); err != nil {
		log.Printf("monitoring prune failed op=node_traffic err=%v", err)
	}
}

func (s *IngestionService) GetLatestMetric(nodeID int64) (*model.NodeMetric, error) {
	return s.repo.GetLatestNodeMetric(nodeID)
}

func (s *IngestionService) GetMetrics(nodeID int64, startMs, endMs int64) ([]model.NodeMetric, error) {
	return s.repo.GetNodeMetrics(nodeID, startMs, endMs)
}
