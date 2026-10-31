package repo

import "go-backend/internal/store/model"

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
