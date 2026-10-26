package handler

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
)

// Return routes: each node traces the route to every reference point and the
// backbone networks on it name the line (CN2 GIA, 163, 9929, CMI ...).
const (
	routeRefreshInterval = 24 * time.Hour
	// Failed traces (old agent, no raw socket) are retried sooner.
	routeRetryInterval  = 6 * time.Hour
	routeCommandTimeout = 20 * time.Second
	routeProbeTimeout   = 270 * time.Second
	routeParallel       = 4
	routeTargetParallel = 3
)

// classifyRoute preserves operator hand-offs instead of letting a fixed
// operator priority hide the actual international transit network.
func classifyRoute(hops []routeHop) string {
	carrier := func(n string) string {
		switch n {
		case "CN2", "CT", "163", "CTGNet":
			return "电信"
		case "9929", "4837", "10099":
			return "联通"
		case "CMIN2", "CMI", "9808":
			return "移动"
		default:
			return n
		}
	}
	var routes []string
	for start := 0; start < len(hops); {
		if hops[start].Net == "" {
			start++
			continue
		}
		operator := carrier(hops[start].Net)
		seen := make(map[string]bool)
		first := make(map[string]int)
		ctBeforeCN2 := false
		end := start
		for end < len(hops) && (hops[end].Net == "" || carrier(hops[end].Net) == operator) {
			n := hops[end].Net
			if !seen[n] {
				first[n] = end
			}
			if n == "CT" && !seen["CN2"] {
				ctBeforeCN2 = true
			}
			seen[n] = true
			end++
		}
		var label string
		switch operator {
		case "电信":
			switch {
			case seen["CTGNet"]:
				label = "电信 CTGNet"
			case seen["CN2"] && !seen["163"] && !ctBeforeCN2:
				label = "电信 CN2 GIA"
			case seen["CN2"]:
				label = "电信 CN2 GT"
			default:
				label = "电信 163"
			}
		case "联通":
			switch {
			case seen["9929"]:
				label = "联通 9929"
			case seen["4837"]:
				label = "联通 4837"
			}
			if seen["10099"] {
				if label != "" {
					domestic := strings.TrimPrefix(label, "联通 ")
					if first["10099"] < first[domestic] {
						label = "联通 10099 → " + domestic
					} else {
						label += " → 10099"
					}
				} else {
					label = "联通 10099"
				}
			}
		case "移动":
			switch {
			case seen["CMIN2"]:
				label = "移动 CMIN2"
			case seen["CMI"]:
				label = "移动 CMI"
			default:
				label = "移动 9808"
			}
		default:
			label = operator
		}
		if len(routes) == 0 || routes[len(routes)-1] != label {
			routes = append(routes, label)
		}
		start = end
	}
	if len(routes) == 0 {
		return "未识别"
	}
	return strings.Join(routes, " → ")
}

type routeHop struct {
	TTL   int     `json:"ttl"`
	IP    string  `json:"ip,omitempty"`
	RttMs float64 `json:"rttMs,omitempty"`
	Net   string  `json:"net,omitempty"`
	ASN   string  `json:"asn,omitempty"`
}

type routePoint struct {
	Name     string `json:"name"`
	Route    string `json:"route,omitempty"`
	Reached  bool   `json:"reached"`
	Protocol string `json:"protocol,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// RouteUpdatedAt is the last successful identification, not the latest
	// attempt. Stale explicitly marks a retained identification.
	RouteUpdatedAt int64 `json:"routeUpdatedAt,omitempty"`
	Stale          bool  `json:"stale,omitempty"`
	// Family is "IPv6" when the route was traced over IPv6.
	Family string     `json:"family,omitempty"`
	Hops   []routeHop `json:"hops,omitempty"`
	// IPv6 is a dual-stack node's IPv6 route to the same point, traced
	// next to the IPv4 one.
	IPv6  *routePoint `json:"ipv6,omitempty"`
	Error string      `json:"error,omitempty"`
	// host is the address traced, so an edited point is traced again.
	host          string
	port          int
	asnIncomplete bool
}

type nodeRoute struct {
	CheckedAt int64        `json:"checkedAt"`
	Points    []routePoint `json:"points"`
	failed    bool
	// agent is the node's agent version when traced, so an upgraded agent
	// (one that gained Traceroute, say) is traced again right away.
	agent    string
	identity string
}

type nodeRouteStore struct {
	mu      sync.RWMutex
	results map[int64]nodeRoute
	running map[int64]chan struct{}
}

func newNodeRouteStore() *nodeRouteStore {
	return &nodeRouteStore{results: make(map[int64]nodeRoute), running: make(map[int64]chan struct{})}
}

// Manual and scheduled requests share an active round, keeping the agent
// below its concurrent trace limit and preventing an older round overwrite.
func (s *nodeRouteStore) begin(nodeID int64) (<-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, ok := s.running[nodeID]; ok {
		return done, false
	}
	done := make(chan struct{})
	s.running[nodeID] = done
	return done, true
}

func (s *nodeRouteStore) finish(nodeID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.running[nodeID])
	delete(s.running, nodeID)
}

func (s *nodeRouteStore) get(nodeID int64) (nodeRoute, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.results[nodeID]
	return r, ok
}

func (s *nodeRouteStore) set(nodeID int64, r nodeRoute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[nodeID] = r
}

// nodeTraceroute runs one Traceroute from a node; tests replace it.
// family 4 / 6 asks for that IP version only, 0 lets the node pick.
var nodeTraceroute = func(h *Handler, nodeID int64, target latencyTarget, family int) (map[string]interface{}, error) {
	payload := map[string]interface{}{"host": target.Host, "port": target.Port, "protocol": "auto"}
	if family != 0 {
		payload["family"] = family
	}
	res, err := h.sendNodeCommandWithTimeout(nodeID, "Traceroute", payload, routeCommandTimeout, false, false)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

func routePointFrom(t latencyTarget, data map[string]interface{}, err error) routePoint {
	p := routePoint{Name: t.Name, host: t.Host, port: t.Port}
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "不在线") || strings.Contains(msg, "离线") || strings.Contains(msg, "未连接"):
			p.Error = "服务器尚未连接，重连后重新检测"
		case strings.Contains(msg, "未知命令"):
			p.Error = "服务器 agent 版本过旧，升级后可检测回程"
		case strings.Contains(msg, "NET_RAW"):
			p.Error = "服务器没有 root / NET_RAW 权限"
		case strings.Contains(msg, "没有 IPv6 地址"):
			p.Error = "服务器只有 IPv6，测试点没有 IPv6 地址"
		case strings.Contains(msg, "没有 IPv4 地址"):
			p.Error = "测试点只有 IPv6 地址，服务器没有 IPv6"
		case strings.Contains(msg, "路由"):
			p.Error = "服务器到测试点没有路由"
		case strings.Contains(msg, "DNS"):
			p.Error = "域名解析失败"
		default:
			p.Error = "检测失败"
		}
		return p
	}
	p.Reached = asBool(data["reached"], false)
	if addr := net.ParseIP(asString(data["ip"])); addr != nil && addr.To4() == nil {
		p.Family = "IPv6"
	}
	rawHops, _ := data["hops"].([]interface{})
	warmHopASNs(rawHops)
	for _, raw := range rawHops {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		ip := canonicalHopIP(asString(m["ip"]))
		p.Hops = append(p.Hops, routeHop{
			TTL:   int(asInt64(m["ttl"], 0)),
			IP:    ip,
			RttMs: asFloat(m["rttMs"], 0),
			Net:   hopNet(ip),
			ASN:   cachedHopASN(ip),
		})
		if publicHopIP(ip) && p.Hops[len(p.Hops)-1].ASN == "" {
			p.asnIncomplete = true
		}
	}
	sort.SliceStable(p.Hops, func(i, j int) bool { return p.Hops[i].TTL < p.Hops[j].TTL })
	p.Protocol = asString(data["protocol"])
	p.Route = classifyRoute(p.Hops)
	p.Detail = routeDetail(p)
	// Keep a complete trace from one protocol. Mixing TTLs from different
	// protocols can fabricate a path that no packet actually followed.
	if fallback, ok := data["fallback"].(map[string]interface{}); ok {
		alternative := routePointFrom(t, map[string]interface{}{
			"ip": fallback["ip"], "reached": fallback["reached"],
			"protocol": fallback["protocol"], "hops": fallback["hops"],
		}, nil)
		if betterRoutePoint(alternative, p) {
			p = alternative
		}
	}
	return p
}

// routeDetail explains a result without inferring a premium line from
// the destination operator or from routers that did not answer.
func routeDetail(p routePoint) string {
	if p.Route != "未识别" {
		if p.asnIncomplete {
			return "部分 ASN 查询暂无结果，已结合已知骨干网段识别；隐藏跳点可能影响 CN2 GIA / GT 等判断"
		}
		return "按已回应路由的骨干网段和 ASN 识别；隐藏跳点可能影响判断，CN2 GIA / GT 为路径特征推断"
	}
	public, resolved := 0, 0
	for _, hop := range p.Hops {
		if publicHopIP(hop.IP) {
			public++
			if hop.ASN != "" {
				resolved++
			}
		}
	}
	switch {
	case public == 0:
		return "未收到公网路由回应，路由器可能过滤了探测包"
	case resolved == 0:
		return "已收到路由回应，但 ASN 查询暂无结果，且未命中已知骨干网段"
	default:
		return "已收到路由回应，但未发现可确认的骨干网；隐藏跳点或普通互联线路可能无法识别"
	}
}

func betterRoutePoint(candidate, current routePoint) bool {
	known := func(p routePoint) bool { return p.Route != "" && p.Route != "未识别" }
	if known(candidate) != known(current) {
		return known(candidate)
	}
	// Prefer TCP when both protocols identify a line, even if ICMP has
	// more responding access routers.
	if known(current) {
		return false
	}
	count := func(p routePoint) int {
		n := 0
		for _, hop := range p.Hops {
			if publicHopIP(hop.IP) {
				n++
			}
		}
		return n
	}
	if a, b := count(candidate), count(current); a != b {
		return a > b
	}
	return candidate.Reached && !current.Reached
}

// traceIPv6Leg traces a point again over IPv6 after an IPv4 trace. Nodes or
// points without IPv6 have no IPv6 leg; an old agent that ignores the
// family answers over IPv4 again, which is dropped.
func traceIPv6Leg(h *Handler, nodeID int64, t latencyTarget) *routePoint {
	data, err := nodeTraceroute(h, nodeID, t, 6)
	if err != nil {
		// Absence of IPv6 is different from a temporary failed probe.
		if strings.Contains(err.Error(), "没有 IPv6 地址") || strings.Contains(err.Error(), "没有 IPv6 路由") {
			return nil
		}
		leg := routePointFrom(t, nil, err)
		leg.Family = "IPv6"
		return &leg
	}
	leg := routePointFrom(t, data, nil)
	if leg.Family != "IPv6" {
		return nil
	}
	return &leg
}

// traceNodeRoutes traces the given local online nodes (all when ids is
// empty) to every enabled reference point. With force false it skips nodes
// whose last trace is fresh and covers the current points.
func (h *Handler) traceNodeRoutes(ctx context.Context, ids []int64, force bool) (map[int64]nodeRoute, error) {
	nodes, err := h.repo.ListMonitorNodes()
	if err != nil {
		return nil, err
	}
	pool := h.latencyPool()
	if len(pool) == 0 {
		return map[int64]nodeRoute{}, nil
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	now := time.Now()
	type job struct {
		targets  []latencyTarget
		agent    string
		name     string
		identity string
	}
	todo := make(map[int64]job)
	for _, n := range nodes {
		if n.Status != 1 || (len(ids) > 0 && !want[n.ID]) {
			continue
		}
		targets := targetsForNode(pool, n.LatencyTargets)
		if len(targets) == 0 {
			continue
		}
		agent := n.Version.String
		if !force {
			if r, ok := h.nodeRoutes.get(n.ID); ok && r.identity == routeNodeIdentity(n) && routeFresh(r, targets, agent, now) {
				continue
			}
		}
		todo[n.ID] = job{targets: targets, agent: agent, name: n.Name, identity: routeNodeIdentity(n)}
	}

	results := make(map[int64]nodeRoute, len(todo))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, routeParallel)
	for id, j := range todo {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return results, ctx.Err()
		}
		wg.Add(1)
		go func(nodeID int64, targets []latencyTarget, agent, name, identity string) {
			defer wg.Done()
			defer func() { <-sem }()
			done, owner := h.nodeRoutes.begin(nodeID)
			if !owner {
				select {
				case <-done:
					if result, ok := h.nodeRoutes.get(nodeID); ok {
						mu.Lock()
						results[nodeID] = result
						mu.Unlock()
					}
				case <-ctx.Done():
				}
				return
			}
			defer h.nodeRoutes.finish(nodeID)
			r := nodeRoute{Points: make([]routePoint, len(targets)), agent: agent, identity: identity}
			// Probes match their own IDs/ports, so a few targets can run in
			// parallel without confusing replies or making nine points take
			// several minutes. Stay below the agent's four-trace limit.
			var targetsWG sync.WaitGroup
			targetSem := make(chan struct{}, routeTargetParallel)
			for i, t := range targets {
				if ctx.Err() != nil {
					break
				}
				targetSem <- struct{}{}
				targetsWG.Add(1)
				go func() {
					defer targetsWG.Done()
					defer func() { <-targetSem }()
					if ctx.Err() != nil {
						return
					}
					data, err := nodeTraceroute(h, nodeID, t, 0)
					r.Points[i] = routePointFrom(t, data, err)
					if ctx.Err() == nil && r.Points[i].Error == "" && r.Points[i].Family == "" {
						r.Points[i].IPv6 = traceIPv6Leg(h, nodeID, t)
					}
				}()
			}
			targetsWG.Wait()
			if ctx.Err() != nil {
				return
			}
			for _, p := range r.Points {
				r.failed = r.failed || routeNeedsRetry(p)
			}
			r.CheckedAt = time.Now().UnixMilli()
			prev, hadPrevious := h.nodeRoutes.get(nodeID)
			if !hadPrevious || prev.identity != identity {
				prev = nodeRoute{}
			}
			r = retainRouteIdentifications(prev, r)
			if hadPrevious && prev.identity == identity {
				if changes := routeChanges(prev, r); len(changes) > 0 {
					h.notifyForNode(nodeID, notifyEventRouteChange, "回程线路变化",
						fmt.Sprintf("服务器：%s\n%s", name, strings.Join(changes, "\n")))
				}
			}
			h.nodeRoutes.set(nodeID, r)
			if err := h.persistNodeRoute(nodeID, r); err != nil {
				log.Printf("save node route snapshot node=%d: %v", nodeID, err)
			}
			mu.Lock()
			results[nodeID] = r
			mu.Unlock()
		}(id, j.targets, j.agent, j.name, j.identity)
	}
	wg.Wait()
	return results, ctx.Err()
}

func routeNeedsRetry(p routePoint) bool {
	return p.Error != "" || p.Route == "" || p.Route == "未识别" || p.Stale || p.asnIncomplete ||
		(p.IPv6 != nil && routeNeedsRetry(*p.IPv6))
}

// routeFresh reports whether a stored trace is recent and was made by the
// node's current agent to the points it measures now.
func routeFresh(r nodeRoute, targets []latencyTarget, agent string, now time.Time) bool {
	if r.agent != agent || len(r.Points) != len(targets) {
		return false
	}
	for i, t := range targets {
		if r.Points[i].Name != t.Name || r.Points[i].host != t.Host || r.Points[i].port != t.Port {
			return false
		}
	}
	every := routeRefreshInterval
	if r.failed {
		every = routeRetryInterval
	}
	for _, p := range r.Points {
		if routeNeedsRetry(p) {
			every = routeRetryInterval
		}
	}
	return now.Sub(time.UnixMilli(r.CheckedAt)) < every
}

// withRoutes adds each point's return route name to a latency result.
func (h *Handler) withRoutes(nodeID int64, p reachProbe) reachProbe {
	r, ok := h.nodeRoutes.get(nodeID)
	if !ok || len(p.Points) == 0 {
		return p
	}
	byName := make(map[string]routePoint, len(r.Points))
	for _, rp := range r.Points {
		byName[rp.Name] = rp
	}
	points := make([]latencyPoint, len(p.Points))
	for i, pt := range p.Points {
		rp := byName[pt.Name]
		if pt.host != "" && (pt.host != rp.host || pt.port != rp.port) {
			rp = routePoint{}
		}
		pt.Route, pt.RouteV6 = rp.Route, ""
		pt.RouteInfo, pt.RouteV6Info = routeLabelInfo(rp, r.CheckedAt), nil
		if rp.IPv6 != nil {
			pt.RouteV6 = rp.IPv6.Route
			pt.RouteV6Info = routeLabelInfo(*rp.IPv6, r.CheckedAt)
		}
		points[i] = pt
	}
	p.Points = points
	return p
}

// nodeRouteGet returns a node's last traces with every hop.
func (h *Handler) nodeRouteGet(w http.ResponseWriter, r *http.Request) {
	h.nodeRouteRespond(w, r, false)
}

// nodeRouteProbe traces a node's return routes now.
func (h *Handler) nodeRouteProbe(w http.ResponseWriter, r *http.Request) {
	h.nodeRouteRespond(w, r, true)
}

func (h *Handler) nodeRouteRespond(w http.ResponseWriter, r *http.Request, probe bool) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if probe {
		if len(h.latencyPool()) == 0 {
			response.WriteJSON(w, response.ErrDefault("还没有设置延迟测试点，请到网站配置添加"))
			return
		}
		// Up to 30 dual-stack targets can outlast the server's default
		// two-minute write timeout. Match the frontend's five-minute limit,
		// reserving time to finish an already dispatched agent command.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
		ctx, cancel := context.WithTimeout(r.Context(), routeProbeTimeout)
		defer cancel()
		if _, err := h.traceNodeRoutes(ctx, []int64{req.ID}, true); err != nil {
			response.WriteJSON(w, response.Err(-2, err.Error()))
			return
		}
	}
	route, ok := h.nodeRoutes.get(req.ID)
	if !ok {
		response.WriteJSON(w, response.OK(nil))
		return
	}
	points := make([]routePoint, len(route.Points))
	for i, p := range route.Points {
		points[i] = routeLegView(p, time.Now())
	}
	route.Points = points
	response.WriteJSON(w, response.OK(route))
}
