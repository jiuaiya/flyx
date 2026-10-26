package handler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func stubRouteASNs(t *testing.T, answers map[string]string) {
	t.Helper()
	oldLookup := hopASNLookup
	hopASNMu.Lock()
	oldCache := hopASNCache
	hopASNCache = make(map[string]hopASNEntry)
	hopASNMu.Unlock()
	byQuery := make(map[string]string)
	for ip, asn := range answers {
		byQuery[asnQueryName(ip)] = asn
	}
	hopASNLookup = func(query string) ([]string, error) {
		if asn := byQuery[query]; asn != "" {
			return []string{asn + " | prefix | CN | apnic"}, nil
		}
		return nil, errors.New("lookup unavailable")
	}
	t.Cleanup(func() {
		hopASNLookup = oldLookup
		hopASNMu.Lock()
		hopASNCache = oldCache
		hopASNMu.Unlock()
	})
}

func TestRouteHopPrefixes(t *testing.T) {
	for _, tt := range []struct{ ip, want string }{
		{"210.14.12.1", "9929"}, {"210.78.12.1", "9929"},
		{"221.183.55.1", "9808"}, {"111.24.1.1", "9808"},
		{"203.22.179.1", "CTGNet"}, {"2400:9380::1", "CTGNet"},
		{"2408:8120::1", "9929"}, {"2402:4F00:F000::1", "CMIN2"},
		{"2402:4f00:1000::1", "CMI"}, {"2401:8a00::1", "10099"},
		{"::ffff:59.43.1.1", "CN2"}, {"59.43.invalid", ""},
	} {
		if got := routeHopNet(tt.ip); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.ip, got, tt.want)
		}
	}
}

func TestHopNetworkASNAttribution(t *testing.T) {
	stubRouteASNs(t, map[string]string{
		"8.8.4.1": "4837", "8.8.4.2": "13335 58807",
		"59.43.1.1": "13335", "223.120.1.1": "58807",
		"2400:4809::1": "4809", "8.8.4.3": "10099", "8.8.4.4": "23764",
	})
	for _, tt := range []struct{ ip, want string }{
		{"8.8.4.1", "4837"}, {"8.8.4.2", "CMIN2"},
		{"59.43.1.1", ""}, {"223.120.1.1", "CMIN2"},
		{"2400:4809::1", "CN2"}, {"8.8.4.3", "10099"}, {"8.8.4.4", "CTGNet"},
		// DNS failure still permits a narrow prefix fallback.
		{"2402:4F00:F000::1", "CMIN2"}, {"202.97.1.1", "163"},
	} {
		if got := hopNet(tt.ip); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.ip, got, tt.want)
		}
	}
	for _, ip := range []string{"10.0.0.1", "127.0.0.1", "100.64.1.1", "fc00::1", "fe80::1", "fe80::1%en0", "", "garbage"} {
		if hopNet(ip) != "" || cachedHopASN(ip) != "" {
			t.Errorf("non-public address %s was attributed", ip)
		}
	}
}

func TestHopASNCacheExpiryAndSingleflight(t *testing.T) {
	stubRouteASNs(t, nil)
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	hopASNLookup = func(string) ([]string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil, errors.New("timeout")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); hopOriginASN("::ffff:8.8.8.8") }()
	}
	<-started
	close(release)
	wg.Wait()
	hopOriginASN("8.8.8.8")
	if calls.Load() != 1 {
		t.Fatalf("duplicate failed lookups: %d", calls.Load())
	}
	hopASNMu.Lock()
	hopASNCache["8.8.8.8"] = hopASNEntry{until: time.Now().Add(-time.Second)}
	hopASNMu.Unlock()
	hopASNLookup = func(string) ([]string, error) {
		calls.Add(1)
		return []string{"unknown | invalid", "13335 58807 | prefix"}, nil
	}
	if got := hopOriginASN("8.8.8.8"); got != "58807" || calls.Load() != 2 {
		t.Fatalf("expired cache: asn=%q, calls=%d", got, calls.Load())
	}
	hopOriginASN("8.8.8.8")
	if calls.Load() != 2 {
		t.Fatal("positive answer was not cached")
	}
}

func TestClassifyRouteExtendedNetworksAndHandoffs(t *testing.T) {
	for _, tt := range []struct {
		nets []string
		want string
	}{
		{[]string{"CTGNet", "CN2", "CT"}, "电信 CTGNet"},
		{[]string{"10099", "", "9929", "4837"}, "联通 10099 → 9929"},
		{[]string{"10099"}, "联通 10099"},
		{[]string{"9929", "10099"}, "联通 9929 → 10099"},
		{[]string{"CMI", "4837"}, "移动 CMI → 联通 4837"},
		{[]string{"CMIN2", "CMI", "9808"}, "移动 CMIN2"},
		{[]string{"9808"}, "移动 9808"},
		{[]string{"CERNET2"}, "CERNET2"},
	} {
		hops := make([]routeHop, len(tt.nets))
		for i, n := range tt.nets {
			hops[i] = routeHop{TTL: i + 1, Net: n}
		}
		if got := classifyRoute(hops); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.nets, got, tt.want)
		}
	}
}

func TestRoutePointProtocolSelectionAndDiagnostics(t *testing.T) {
	stubRouteASNs(t, map[string]string{"8.8.8.8": "13335"})
	trace := func(protocol string, ips ...string) map[string]interface{} {
		hops := make([]interface{}, len(ips))
		for i, ip := range ips {
			hops[i] = map[string]interface{}{"ttl": i + 1, "ip": ip}
		}
		return map[string]interface{}{"ip": "1.1.1.1", "protocol": protocol, "hops": hops}
	}
	primary := trace("tcp", "10.0.0.1", "8.8.8.8")
	primary["fallback"] = trace("icmp", "", "59.43.1.1")
	p := routePointFrom(latencyTarget{Name: "电信"}, primary, nil)
	if p.Protocol != "icmp" || p.Route != "电信 CN2 GIA" || len(p.Hops) != 2 || p.Hops[0].IP != "" {
		t.Fatalf("fallback selection merged paths or lost classification: %+v", p)
	}
	primary = trace("tcp", "223.120.1.1")
	primary["fallback"] = trace("icmp", "202.97.1.1")
	p = routePointFrom(latencyTarget{Name: "移动"}, primary, nil)
	if p.Protocol != "tcp" || p.Route != "移动 CMI" {
		t.Fatalf("recognized TCP trace was replaced: %+v", p)
	}
	for _, tt := range []struct {
		ips    []string
		detail string
	}{
		{nil, "未收到公网路由回应"},
		{[]string{"8.8.4.4"}, "ASN 查询暂无结果"},
		{[]string{"8.8.8.8"}, "未发现可确认的骨干网"},
	} {
		p := routePointFrom(latencyTarget{}, trace("tcp", tt.ips...), nil)
		if p.Route != "未识别" || !strings.Contains(p.Detail, tt.detail) {
			t.Errorf("diagnostic: %+v", p)
		}
	}
}

func TestRouteFreshUnknownLegAndPortChanges(t *testing.T) {
	now := time.Now()
	targets := []latencyTarget{{Name: "电信", Host: "ct.example", Port: 443}}
	r := nodeRoute{CheckedAt: now.Add(-7 * time.Hour).UnixMilli(), agent: "v1", Points: []routePoint{{
		Name: "电信", host: "ct.example", port: 443, Route: "电信 163",
	}}}
	if !routeFresh(r, targets, "v1", now) {
		t.Fatal("recognized trace expired early")
	}
	r.Points[0].asnIncomplete = true
	if routeFresh(r, targets, "v1", now) {
		t.Fatal("partial ASN failure must retry in six hours")
	}
	r.Points[0].asnIncomplete = false
	r.Points[0].IPv6 = &routePoint{Route: "未识别"}
	if routeFresh(r, targets, "v1", now) {
		t.Fatal("unknown IPv6 trace must retry in six hours")
	}
	r.Points[0].IPv6 = nil
	r.Points[0].Route = "未识别"
	if routeFresh(r, targets, "v1", now) {
		t.Fatal("unknown trace cached for a day")
	}
	r.CheckedAt = now.UnixMilli()
	targets[0].Port = 80
	if routeFresh(r, targets, "v1", now) {
		t.Fatal("edited TCP port must invalidate trace")
	}
}

func TestTraceNodeRoutesCancellationKeepsPreviousResult(t *testing.T) {
	stubRouteASNs(t, nil)
	h, r := setupLatencyHandler(t, `[{"name":"电信","host":"ct.example","port":443,"enabled":true}]`)
	node := mustCreateLatencyNode(t, r, "online", 1, 0)
	h.nodeRoutes.set(node, nodeRoute{CheckedAt: 123})
	old := nodeTraceroute
	t.Cleanup(func() { nodeTraceroute = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	nodeTraceroute = func(_ *Handler, _ int64, target latencyTarget, _ int) (map[string]interface{}, error) {
		calls++
		if target.Port != 443 {
			t.Errorf("lost TCP target port: %+v", target)
		}
		cancel()
		return nil, nil
	}
	if _, err := h.traceNodeRoutes(ctx, []int64{node}, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p, _ := h.nodeRoutes.get(node); p.CheckedAt != 123 || calls != 1 {
		t.Fatalf("canceled round replaced previous result: %+v, calls=%d", p, calls)
	}
}

func TestTraceNodeRouteTargetConcurrency(t *testing.T) {
	stubRouteASNs(t, nil)
	h, r := setupLatencyHandler(t, `[
		{"name":"一","host":"a.example","port":80,"enabled":true},
		{"name":"二","host":"b.example","port":443,"enabled":true},
		{"name":"三","host":"c.example","port":80,"enabled":true},
		{"name":"四","host":"d.example","port":80,"enabled":true}]`)
	node := mustCreateLatencyNode(t, r, "online", 1, 0)
	old := nodeTraceroute
	t.Cleanup(func() { nodeTraceroute = old })
	var active, peak atomic.Int64
	firstWave := make(chan struct{})
	var release sync.Once
	nodeTraceroute = func(_ *Handler, _ int64, target latencyTarget, family int) (map[string]interface{}, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for was := peak.Load(); n > was && !peak.CompareAndSwap(was, n); was = peak.Load() {
		}
		if n == int64(routeTargetParallel) {
			release.Do(func() { close(firstWave) })
		}
		select {
		case <-firstWave:
		case <-time.After(2 * time.Second):
			t.Error("targets were not probed in parallel")
		}
		if family == 6 {
			return nil, errors.New("目标没有 IPv6 地址")
		}
		return map[string]interface{}{"protocol": "tcp", "reached": true, "hops": []interface{}{
			map[string]interface{}{"ttl": 1, "ip": "202.97.1.1"},
		}}, nil
	}
	results, err := h.traceNodeRoutes(context.Background(), []int64{node}, true)
	if err != nil || len(results[node].Points) != 4 || peak.Load() != int64(routeTargetParallel) {
		t.Fatalf("parallel round: %+v, peak=%d, err=%v", results, peak.Load(), err)
	}
	for i, name := range []string{"一", "二", "三", "四"} {
		if results[node].Points[i].Name != name || results[node].Points[i].Route != "电信 163" {
			t.Fatalf("target order lost: %+v", results[node].Points)
		}
	}
}

func TestNodeRouteStoreSharesActiveRound(t *testing.T) {
	s := newNodeRouteStore()
	done, owner := s.begin(1)
	other, duplicateOwner := s.begin(1)
	if !owner || duplicateOwner || done != other {
		t.Fatal("concurrent round was not shared")
	}
	s.set(1, nodeRoute{CheckedAt: 123})
	s.finish(1)
	select {
	case <-other:
	default:
		t.Fatal("waiting request was not released")
	}
	if got, _ := s.get(1); got.CheckedAt != 123 {
		t.Fatal("lost completed result")
	}
	_, owner = s.begin(1)
	if !owner {
		t.Fatal("completed round blocked the next trace")
	}
	s.finish(1)
}
