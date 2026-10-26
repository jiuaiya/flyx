package handler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/store/repo"
)

func TestRetainRouteIdentificationAfterFailures(t *testing.T) {
	start := time.Now().Add(-time.Hour).UnixMilli()
	old := nodeRoute{CheckedAt: start, Points: []routePoint{{Name: "电信", host: "ct.example", port: 443, Route: "电信 CN2 GIA", RouteUpdatedAt: start}}}
	for _, failed := range []routePoint{
		{Name: "电信", host: "ct.example", port: 443, Error: "检测失败"},
		{Name: "电信", host: "ct.example", port: 443, Route: "未识别", Hops: []routeHop{{TTL: 1, IP: "10.0.0.1"}}, Detail: "未收到公网回应"},
		{Name: "电信", host: "ct.example", port: 443, Route: "电信 163", asnIncomplete: true},
	} {
		next := retainRouteIdentifications(old, nodeRoute{CheckedAt: start + 1000, Points: []routePoint{failed}})
		p := next.Points[0]
		if p.Route != old.Points[0].Route || !p.Stale || p.RouteUpdatedAt != start || p.Error != failed.Error || len(p.Hops) != len(failed.Hops) {
			t.Fatalf("lost history or replaced the current probe: %+v", p)
		}
		if !routeNeedsRetry(p) {
			t.Fatal("retained result must retry")
		}
		if changes := routeChanges(old, next); len(changes) != 0 {
			t.Fatalf("false alert: %v", changes)
		}
		// Another failed attempt must not make the old result look new.
		again := retainRouteIdentifications(next, nodeRoute{CheckedAt: start + 2000, Points: []routePoint{failed}})
		if again.Points[0].RouteUpdatedAt != start {
			t.Fatal("failed probe advanced the successful timestamp")
		}
		recovered := retainRouteIdentifications(again, nodeRoute{CheckedAt: start + 3000, Points: []routePoint{{Name: "电信", host: "ct.example", port: 443, Route: "电信 163"}}})
		if p := recovered.Points[0]; p.Stale || p.RouteUpdatedAt != start+3000 || p.Error != "" {
			t.Fatalf("successful recovery: %+v", p)
		}
		if changes := routeChanges(again, recovered); len(changes) != 1 {
			t.Fatalf("missed the actual route change after a failed probe: %v", changes)
		}
	}
}

func TestRetainRouteIdentificationMatchesTargetAndFamily(t *testing.T) {
	old := nodeRoute{identity: "server-a", CheckedAt: 100, Points: []routePoint{{Name: "旧名称", host: "a.example", port: 443, Route: "电信 163", RouteUpdatedAt: 100, IPv6: &routePoint{Name: "旧名称", Family: "IPv6", Route: "联通 9929", RouteUpdatedAt: 90}}}}
	for _, tt := range []struct {
		name, identity, host, family string
		port                         int
		want                         string
	}{
		{"rename", "server-a", "a.example", "", 443, "电信 163"},
		{"new host", "server-a", "b.example", "", 443, "未识别"},
		{"new port", "server-a", "a.example", "", 80, "未识别"},
		{"new server", "server-b", "a.example", "", 443, "未识别"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next := retainRouteIdentifications(old, nodeRoute{identity: tt.identity, CheckedAt: 200, Points: []routePoint{{Name: "新名称", host: tt.host, port: tt.port, Family: tt.family, Route: "未识别"}}})
			if p := next.Points[0]; p.Route != tt.want || p.Name != "新名称" || p.Family != tt.family {
				t.Fatalf("wrong target/family: %+v", p)
			}
		})
	}
	old.Points[0].IPv6 = nil
	if p := retainRouteIdentifications(old, nodeRoute{identity: "server-a", CheckedAt: 200, Points: []routePoint{{Name: "新名称", host: "a.example", port: 443, Family: "IPv6", Route: "未识别"}}}).Points[0]; p.Route != "电信 163" || !p.Stale || p.IPv6 == nil || p.IPv6.Route != "未识别" || p.IPv6.Stale {
		t.Fatalf("family fallback mixed the old label into IPv6: %+v / %+v", p, p.IPv6)
	}
}

func TestRetainDualStackLabelsAfterPrimaryProbeFails(t *testing.T) {
	old := nodeRoute{CheckedAt: 100, Points: []routePoint{{Name: "三网", host: "a.example", port: 80, Route: "电信 163", RouteUpdatedAt: 100, IPv6: &routePoint{Name: "三网", Family: "IPv6", Route: "移动 CMI", RouteUpdatedAt: 90}}}}
	next := retainRouteIdentifications(old, nodeRoute{CheckedAt: 200, Points: []routePoint{{Name: "三网", host: "a.example", port: 80, Error: "域名解析失败"}}})
	p := next.Points[0]
	if p.Route != "电信 163" || !p.Stale || p.IPv6 == nil || p.IPv6.Route != "移动 CMI" || !p.IPv6.Stale || p.IPv6.RouteUpdatedAt != 90 {
		t.Fatalf("lost a family: %+v / %+v", p, p.IPv6)
	}
	// Temporarily missing IPv6 keeps its old identification explicitly stale.
	next = retainRouteIdentifications(old, nodeRoute{CheckedAt: 200, Points: []routePoint{{Name: "三网", host: "a.example", port: 80, Route: "电信 163"}}})
	if next.Points[0].IPv6 == nil || !next.Points[0].IPv6.Stale || next.Points[0].IPv6.Route != "移动 CMI" {
		t.Fatal("lost the last known IPv6 identification")
	}
	next = retainRouteIdentifications(old, nodeRoute{CheckedAt: 200, Points: []routePoint{{Name: "三网", host: "a.example", port: 80, Family: "IPv6", Route: "移动 CMIN2"}}})
	if p := next.Points[0]; p.Route != "电信 163" || !p.Stale || p.IPv6 == nil || p.IPv6.Route != "移动 CMIN2" || p.IPv6.Stale || p.IPv6.RouteUpdatedAt != 200 {
		t.Fatalf("wrong dual-stack auto-family fallback: %+v / %+v", p, p.IPv6)
	}
}

func TestTraceIPv6LegDistinguishesFailureFromMissingFamily(t *testing.T) {
	old := nodeTraceroute
	t.Cleanup(func() { nodeTraceroute = old })
	for _, tt := range []struct {
		msg    string
		absent bool
	}{
		{"目标没有 IPv6 地址", true}, {"节点没有 IPv6 路由", true}, {"command timeout", false},
	} {
		nodeTraceroute = func(*Handler, int64, latencyTarget, int) (map[string]interface{}, error) {
			return nil, errors.New(tt.msg)
		}
		leg := traceIPv6Leg(nil, 1, latencyTarget{Name: "三网"})
		if (leg == nil) != tt.absent {
			t.Fatalf("%s: %+v", tt.msg, leg)
		}
		if leg != nil && (leg.Family != "IPv6" || leg.Error == "") {
			t.Fatalf("failed family lost: %+v", leg)
		}
	}
}

func TestNodeRouteSnapshotSurvivesRestartAndFailure(t *testing.T) {
	stubRouteASNs(t, nil)
	path := t.TempDir() + "/routes.db"
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if err := r.UpsertConfig(latencyTargetsConfigKey, `[{"name":"三网","host":"a.example","port":443,"enabled":true}]`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	h := New(r, "test-secret")
	node := mustCreateLatencyNode(t, r, "online", 1, 0)
	old := nodeTraceroute
	t.Cleanup(func() { nodeTraceroute = old })
	nodeTraceroute = func(_ *Handler, _ int64, _ latencyTarget, family int) (map[string]interface{}, error) {
		ip, hop := "1.1.1.1", "202.97.1.1"
		if family == 6 {
			ip, hop = "240e::1", "2408:8120::1"
		}
		return map[string]interface{}{"ip": ip, "protocol": "tcp", "hops": []interface{}{map[string]interface{}{"ttl": 1, "ip": hop}}}, nil
	}
	results, err := h.traceNodeRoutes(context.Background(), []int64{node}, true)
	if err != nil {
		t.Fatal(err)
	}
	identifiedAt := results[node].Points[0].RouteUpdatedAt
	if identifiedAt <= 0 {
		t.Fatal("successful identification has no date")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h = New(r, "test-secret")
	got, ok := h.nodeRoutes.get(node)
	if !ok || got.Points[0].Route != "电信 163" || got.Points[0].IPv6 == nil || got.Points[0].IPv6.Route != "联通 9929" || got.Points[0].RouteUpdatedAt != identifiedAt || got.Points[0].port != 443 {
		t.Fatalf("snapshot not restored: %+v", got)
	}
	// Persist an unsuccessful new attempt, then restart again.
	nodeTraceroute = func(*Handler, int64, latencyTarget, int) (map[string]interface{}, error) {
		return nil, errors.New("timeout")
	}
	if _, err := h.traceNodeRoutes(context.Background(), []int64{node}, true); err != nil {
		t.Fatal(err)
	}
	h = New(r, "test-secret")
	got, ok = h.nodeRoutes.get(node)
	if !ok || !got.failed || !got.Points[0].Stale || !got.Points[0].IPv6.Stale || got.Points[0].RouteUpdatedAt != identifiedAt {
		t.Fatalf("failed snapshot lost the good identification: %+v", got)
	}
	p := h.withRoutes(node, latencyProbe([]latencyPoint{{Name: "三网", host: "a.example", port: 443}}, time.Now().UnixMilli())).Points[0]
	if p.Route != "电信 163" || p.RouteInfo == nil || !p.RouteInfo.Stale || p.RouteV6 != "联通 9929" || p.RouteV6Info == nil || !p.RouteV6Info.Stale {
		t.Fatalf("card metadata not restored: %+v", p)
	}
	p = h.withRoutes(node, latencyProbe([]latencyPoint{{Name: "三网", host: "a.example", port: 80}}, time.Now().UnixMilli())).Points[0]
	if p.Route != "" || p.RouteInfo != nil {
		t.Fatal("old target route displayed for a different port")
	}
	if err := r.UpsertConfig(latencyTargetsConfigKey, `[{"name":"三网","host":"b.example","port":443,"enabled":true}]`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, ok := New(r, "test-secret").nodeRoutes.get(node); ok {
		t.Fatal("restored old target after configuration changed")
	}
}

func TestRestoreNodeRouteIgnoresInvalidSnapshot(t *testing.T) {
	h, r := setupLatencyHandler(t, `[{"name":"三网","host":"a.example","port":443,"enabled":true}]`)
	node := mustCreateLatencyNode(t, r, "online", 1, 0)
	for _, data := range []string{`{`, `{"version":2}`, `{"version":1,"result":{"checkedAt":1,"points":[{}]},"targets":[]}`} {
		if err := r.SaveNodeRouteSnapshot(node, data); err != nil {
			t.Fatal(err)
		}
		h = New(r, "test-secret")
		if _, ok := h.nodeRoutes.get(node); ok {
			t.Fatalf("accepted invalid snapshot %q", data)
		}
	}
}

func TestOldRouteLabelIsMarkedStaleWithoutMutatingStore(t *testing.T) {
	now := time.Now()
	p := routePoint{Name: "三网", Route: "联通 9929", RouteUpdatedAt: now.Add(-25 * time.Hour).UnixMilli()}
	view := routeLegView(p, now)
	if !view.Stale || p.Stale || view.Route != p.Route || view.RouteUpdatedAt != p.RouteUpdatedAt {
		t.Fatalf("invalid aged view: %+v", view)
	}
}

func TestReconnectImmediatelyRetriesUnidentifiedRoute(t *testing.T) {
	stubRouteASNs(t, nil)
	h, r := setupLatencyHandler(t, `[{"name":"三网","host":"a.example","port":80,"enabled":true}]`)
	node := mustCreateLatencyNode(t, r, "online", 1, 0)
	other := mustCreateLatencyNode(t, r, "other", 1, 0)
	nodes, err := r.ListMonitorNodes()
	if err != nil {
		t.Fatal(err)
	}
	var identity string
	for _, n := range nodes {
		if n.ID == node {
			identity = routeNodeIdentity(n)
		}
	}
	h.nodeRoutes.set(node, nodeRoute{CheckedAt: time.Now().UnixMilli(), identity: identity, failed: true, Points: []routePoint{{Name: "三网", host: "a.example", port: 80, Error: "服务器尚未连接，重连后重新检测"}}})
	h.nodeRoutes.set(other, nodeRoute{CheckedAt: 123})
	stubLatencyPing(t, func(int64, string) (map[string]interface{}, error) { return okPing(12), nil })
	old := nodeTraceroute
	t.Cleanup(func() { nodeTraceroute = old })
	var calls atomic.Int64
	nodeTraceroute = func(_ *Handler, id int64, _ latencyTarget, family int) (map[string]interface{}, error) {
		calls.Add(1)
		if id != node {
			t.Errorf("unrelated node traced: %d", id)
		}
		if family == 6 {
			return nil, errors.New("目标没有 IPv6 地址")
		}
		return map[string]interface{}{"ip": "1.1.1.1", "hops": []interface{}{map[string]interface{}{"ttl": 1, "ip": "202.97.1.1"}}}, nil
	}
	if err := h.probeReconnectedNodes(context.Background(), []int64{node}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.nodeRoutes.get(node); calls.Load() != 2 || got.Points[0].Route != "电信 163" || got.Points[0].Stale {
		t.Fatalf("failed route was not retried: %+v, calls=%d", got, calls.Load())
	}
	if got, _ := h.nodeRoutes.get(other); got.CheckedAt != 123 {
		t.Fatal("unrelated node was traced")
	}
	if err := h.probeReconnectedNodeRoutes(context.Background(), []int64{node}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("a restored/fresh valid route was unnecessarily traced again")
	}
	if err := r.UpsertConfig(latencyTargetsConfigKey, `[{"name":"三网","host":"a.example","port":80,"enabled":true},{"name":"三网2","host":"b.example","port":80,"enabled":true}]`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	partial, _ := h.nodeRoutes.get(node)
	partial.Points = append(partial.Points, routePoint{Name: "三网2", host: "b.example", port: 80, Error: "服务器尚未连接，重连后重新检测"})
	h.nodeRoutes.set(node, partial)
	if err := h.probeReconnectedNodeRoutes(context.Background(), []int64{node}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.nodeRoutes.get(node); calls.Load() != 6 || len(got.Points) != 2 || got.Points[1].Route != "电信 163" {
		t.Fatalf("partial startup failure was not retried: %+v, calls=%d", got, calls.Load())
	}
}
