package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCN2ClassificationUsesBackboneEvidence(t *testing.T) {
	stubRouteASNs(t, map[string]string{
		"218.30.48.97": "4134", "222.247.26.14": "4134", "202.103.124.48": "4134",
		"140.249.80.214": "58540", "2400:4809::1": "4809", "240e:1::1": "4134",
	})
	for _, tc := range []struct {
		name    string
		ips     []string
		reached bool
		want    string
	}{
		{"VMISS Shandong IPv4", []string{"218.30.48.97", "59.43.246.117", "59.43.39.125", "59.43.22.5", "59.43.80.141", "140.249.80.214"}, true, "电信 CN2 GIA"},
		{"VMISS Hunan IPv4", []string{"218.30.48.97", "59.43.246.177", "59.43.43.22", "59.43.69.6", "222.247.26.14", "202.103.124.48"}, true, "电信 CN2 GIA"},
		{"one CN2 interface", []string{"218.30.48.97", "59.43.246.117", "140.249.80.214"}, true, "电信 CN2（GIA/GT 未确定）"},
		{"repeated interface", []string{"59.43.246.117", "59.43.246.117", "140.249.80.214"}, true, "电信 CN2（GIA/GT 未确定）"},
		{"trace did not reach target", []string{"218.30.48.97", "59.43.246.117", "59.43.39.125"}, false, "电信 CN2（GIA/GT 未确定）"},
		{"observed 163 backbone", []string{"202.97.12.1", "59.43.246.117", "59.43.39.125"}, true, "电信 CN2 GT"},
		{"IPv6 generic AS4134 is not GT evidence", []string{"240e:1::1", "2400:4809::1"}, true, "电信 CN2（GIA/GT 未确定）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hops := make([]interface{}, 0, len(tc.ips))
			for i, ip := range tc.ips {
				hops = append(hops, map[string]interface{}{"ttl": i + 1, "ip": ip})
			}
			p := routePointFrom(latencyTarget{Name: tc.name}, map[string]interface{}{"reached": tc.reached, "protocol": "tcp", "hops": hops}, nil)
			if p.Route != tc.want {
				t.Fatalf("got %q, want %q; hops=%+v", p.Route, tc.want, p.Hops)
			}
			if strings.Contains(tc.want, "未确定") && !routeNeedsRetry(p) {
				t.Fatal("uncertain CN2 classification must be retried")
			}
		})
	}
}

func TestCN2GradeCorrectionDespiteUnrelatedMissingASNs(t *testing.T) {
	old := nodeRoute{CheckedAt: 100, Points: []routePoint{{Name: "电信", host: "ct.example", port: 443, Route: "电信 CN2 GT", RouteUpdatedAt: 100}}}
	for _, want := range []string{"电信 CN2 GIA", routeCN2Uncertain} {
		next := retainRouteIdentifications(old, nodeRoute{CheckedAt: 200, Points: []routePoint{{Name: "电信", host: "ct.example", port: 443, Route: want, asnIncomplete: true}}})
		if p := next.Points[0]; p.Route != want || p.Stale || p.RouteUpdatedAt != 200 {
			t.Fatalf("old grade survived corrected backbone evidence: %+v", p)
		}
	}
}

func TestCN2CachedGradeReclassifiedWithoutChangingObservationTime(t *testing.T) {
	stubRouteASNs(t, nil)
	h, r := setupLatencyHandler(t, `[{"name":"电信","host":"ct.example","port":443,"enabled":true}]`)
	id := mustCreateLatencyNode(t, r, "VMISS", 1, 0)
	node, err := r.GetNodeByID(id)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now().Add(-time.Hour).UnixMilli()
	hops := []routeHop{{TTL: 3, IP: "218.30.48.97", Net: "CT", ASN: "4134"}, {TTL: 4, IP: "59.43.246.117", Net: "CN2"}, {TTL: 5, IP: "59.43.39.125", Net: "CN2"}}
	old := nodeRoute{CheckedAt: observed, identity: routeNodeIdentity(*node), Points: []routePoint{{Name: "电信", host: "ct.example", port: 443, Route: "电信 CN2 GT", RouteUpdatedAt: observed, Reached: true, Hops: hops, asnIncomplete: true, IPv6: &routePoint{Name: "电信", Family: "IPv6", Route: "电信 CN2 GIA", RouteUpdatedAt: observed - 1000, Stale: true, Error: "检测失败"}}}}
	if err := h.persistNodeRoute(id, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		restarted := New(r, "test-secret")
		got, ok := restarted.nodeRoutes.get(id)
		if !ok || got.CheckedAt != observed || got.Points[0].Route != "电信 CN2 GIA" || got.Points[0].RouteUpdatedAt != observed {
			t.Fatalf("cached label/time incorrect: %+v", got)
		}
		v6 := got.Points[0].IPv6
		if v6 == nil || v6.Route != routeCN2Uncertain || !v6.Stale || v6.RouteUpdatedAt != observed-1000 {
			t.Fatalf("stale grade presented as certain: %+v", v6)
		}
		rows, err := r.ListNodeRouteSnapshots()
		if err != nil || len(rows) != 1 {
			t.Fatalf("snapshots: %+v %v", rows, err)
		}
		var saved savedNodeRoute
		if err := json.Unmarshal([]byte(rows[0].Data), &saved); err != nil || saved.Result.Points[0].Route != "电信 CN2 GIA" {
			t.Fatalf("corrected snapshot was not persisted: %+v %v", saved, err)
		}
	}
}

func TestCN2FallbackPrefersSufficientEvidenceOnSameBackbone(t *testing.T) {
	stubRouteASNs(t, nil)
	trace := func(ips ...string) map[string]interface{} {
		hops := make([]interface{}, 0, len(ips))
		for i, ip := range ips {
			hops = append(hops, map[string]interface{}{"ttl": i + 1, "ip": ip})
		}
		return map[string]interface{}{"reached": true, "hops": hops}
	}
	tcp := trace("59.43.246.117")
	tcp["protocol"] = "tcp"
	icmp := trace("59.43.246.117", "59.43.39.125")
	icmp["protocol"] = "icmp"
	tcp["fallback"] = icmp
	p := routePointFrom(latencyTarget{}, tcp, nil)
	if p.Route != "电信 CN2 GIA" || p.Protocol != "icmp" || len(p.Hops) != 2 {
		t.Fatalf("more complete CN2 evidence discarded: %+v", p)
	}
}
