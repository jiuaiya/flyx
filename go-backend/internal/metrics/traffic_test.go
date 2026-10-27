package metrics

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestNodeTrafficDelta(t *testing.T) {
	previous := &model.NodeMetric{Timestamp: 100000, Uptime: 10000, NetInBytes: 54000000000, NetOutBytes: 51000000000}
	for _, tc := range []struct {
		name     string
		current  model.NodeMetric
		in, out  int64
		accepted bool
	}{
		{"normal", model.NodeMetric{Timestamp: 105000, Uptime: 10005, NetInBytes: 54000000100, NetOutBytes: 51000000200}, 100, 200, true},
		{"delayed sample", model.NodeMetric{Timestamp: 105000, Uptime: 9995, NetInBytes: 53999920000, NetOutBytes: 50999900000}, 0, 0, false},
		{"same second stale sample", model.NodeMetric{Timestamp: 100001, Uptime: 10000, NetInBytes: 53999920000, NetOutBytes: 50999900000}, 0, 0, false},
		{"interface reset", model.NodeMetric{Timestamp: 105000, Uptime: 10005, NetInBytes: 53000000000, NetOutBytes: 50000000000}, 0, 0, true},
		{"one direction reset", model.NodeMetric{Timestamp: 105000, Uptime: 10005, NetInBytes: 53000000000, NetOutBytes: 51000000200}, 0, 200, true},
		{"confirmed reboot", model.NodeMetric{Timestamp: 160000, Uptime: 30, NetInBytes: 1000, NetOutBytes: 2000}, 1000, 2000, true},
		{"uptime read failure", model.NodeMetric{Timestamp: 105000, NetInBytes: 54000000100, NetOutBytes: 51000000200}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, out, accepted := NodeTrafficDelta(previous, &tc.current)
			if in != tc.in || out != tc.out || accepted != tc.accepted {
				t.Fatalf("got (%d, %d, %t), want (%d, %d, %t)", in, out, accepted, tc.in, tc.out, tc.accepted)
			}
		})
	}
}

func TestStaleTrafficDoesNotReplaceBaseline(t *testing.T) {
	s := NewIngestionService(nil)
	s.RecordNodeMetric(2, SystemInfo{Uptime: 10000, BytesReceived: 54000000000, BytesTransmitted: 51000000000})
	s.RecordNodeMetric(2, SystemInfo{Uptime: 9995, BytesReceived: 53999920000, BytesTransmitted: 50999900000})
	s.RecordNodeMetric(2, SystemInfo{Uptime: 10005, BytesReceived: 54000000100, BytesTransmitted: 51000000200})
	var in, out int64
	for _, delta := range s.traffic {
		in += delta[0]
		out += delta[1]
	}
	if in != 100 || out != 200 || len(s.nodeBuffer) != 2 {
		t.Fatalf("stale metric affected traffic: in=%d out=%d samples=%d", in, out, len(s.nodeBuffer))
	}
}
