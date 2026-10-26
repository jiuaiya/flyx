package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"go-backend/internal/store/model"
)

type routeLabelMetadata struct {
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	CheckedAt int64  `json:"checkedAt,omitempty"`
	Stale     bool   `json:"stale,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Family    string `json:"family,omitempty"`
}

func identifiedRoute(p routePoint) bool {
	return p.Route != "" && p.Route != "未识别"
}

type routeTargetKey struct {
	host string
	port int
}

func routeNodeIdentity(n model.Node) string {
	// A restored database or edited server can reuse a node ID for another
	// machine. Never reuse that machine's old return-route identification.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", n.CreatedTime, n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String))))
}

func previousRouteLeg(p routePoint, family string) routePoint {
	if p.Family == family {
		return p
	}
	if p.IPv6 != nil && p.IPv6.Family == family {
		return *p.IPv6
	}
	return routePoint{}
}

// Keep the latest probe's hops and errors, but retain a separately dated
// route label when that probe cannot identify the route. No old hops are
// presented as evidence for the latest attempt.
func retainRouteIdentifications(prev, next nodeRoute) nodeRoute {
	if prev.identity != next.identity {
		prev = nodeRoute{}
	}
	byTarget := make(map[routeTargetKey]routePoint)
	for _, p := range prev.Points {
		byTarget[routeTargetKey{p.host, p.port}] = p
	}
	retain := func(old, current routePoint) routePoint {
		uncertainChange := current.asnIncomplete && identifiedRoute(old) && current.Route != old.Route
		if current.Error == "" && identifiedRoute(current) && !uncertainChange {
			current.RouteUpdatedAt, current.Stale = next.CheckedAt, false
		} else if identifiedRoute(old) {
			current.Route, current.Stale = old.Route, true
			current.RouteUpdatedAt = old.RouteUpdatedAt
			if current.RouteUpdatedAt == 0 {
				current.RouteUpdatedAt = prev.CheckedAt
			}
			if uncertainChange && current.Error == "" {
				current.Detail = "本次 ASN 查询不完整，保留上次识别结果"
			}
		}
		return current
	}
	for i, p := range next.Points {
		old := byTarget[routeTargetKey{p.host, p.port}]
		// If auto-family falls back to IPv6, retain the previously observed
		// IPv4 label as an explicitly stale leg next to the new IPv6 trace.
		if p.Family == "IPv6" && old.Family == "" && identifiedRoute(old) {
			ipv6 := p
			p = routePoint{Name: p.Name, host: p.host, port: p.port, Error: "本次没有可用的 IPv4 回程结果", IPv6: &ipv6}
		}
		// A failed auto-family probe has no selected address. Preserve the
		// old family rather than relabelling an IPv6-only node as IPv4.
		if p.Error != "" && p.Family == "" {
			p.Family = old.Family
		}
		if p.Family != "IPv6" && p.IPv6 == nil && identifiedRoute(previousRouteLeg(old, "IPv6")) {
			reason := "本次没有可用的 IPv6 回程结果"
			if p.Error != "" {
				reason = "本次未完成 IPv6 检测"
			}
			p.IPv6 = &routePoint{Name: p.Name, Family: "IPv6", Error: reason, host: p.host, port: p.port}
		}
		p = retain(previousRouteLeg(old, p.Family), p)
		if p.IPv6 != nil {
			leg := retain(previousRouteLeg(old, "IPv6"), *p.IPv6)
			p.IPv6 = &leg
		}
		next.Points[i] = p
	}
	return next
}

func routeLegView(p routePoint, now time.Time) routePoint {
	if identifiedRoute(p) && p.RouteUpdatedAt > 0 && now.Sub(time.UnixMilli(p.RouteUpdatedAt)) >= routeRefreshInterval && !p.Stale {
		p.Stale = true
		p.Detail = "线路结果较早，等待重新检测"
	}
	if p.IPv6 != nil {
		leg := routeLegView(*p.IPv6, now)
		p.IPv6 = &leg
	}
	return p
}

func routeLabelInfo(p routePoint, checkedAt int64) *routeLabelMetadata {
	if p.Name == "" {
		return nil
	}
	p = routeLegView(p, time.Now())
	family := p.Family
	if family == "" {
		family = "IPv4"
	}
	reason := p.Error
	if reason == "" {
		reason = p.Detail
	}
	return &routeLabelMetadata{UpdatedAt: p.RouteUpdatedAt, CheckedAt: checkedAt, Stale: p.Stale, Reason: reason, Family: family}
}

// Private cache fields are persisted explicitly, without exposing target
// addresses or scheduling bookkeeping through the public route API.
type savedRouteTarget struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	ASNIncomplete     bool   `json:"asnIncomplete,omitempty"`
	IPv6ASNIncomplete bool   `json:"ipv6ASNIncomplete,omitempty"`
}

type savedNodeRoute struct {
	Version  int                `json:"version"`
	Result   nodeRoute          `json:"result"`
	Agent    string             `json:"agent"`
	Identity string             `json:"identity"`
	Failed   bool               `json:"failed,omitempty"`
	Targets  []savedRouteTarget `json:"targets"`
}

func (h *Handler) persistNodeRoute(nodeID int64, r nodeRoute) error {
	saved := savedNodeRoute{Version: 1, Result: r, Agent: r.agent, Identity: r.identity, Failed: r.failed}
	for _, p := range r.Points {
		t := savedRouteTarget{Host: p.host, Port: p.port, ASNIncomplete: p.asnIncomplete}
		if p.IPv6 != nil {
			t.IPv6ASNIncomplete = p.IPv6.asnIncomplete
		}
		saved.Targets = append(saved.Targets, t)
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return h.repo.SaveNodeRouteSnapshot(nodeID, string(data))
}

func (h *Handler) restoreNodeRoutes() {
	if h.repo == nil {
		return
	}
	snapshots, err := h.repo.ListNodeRouteSnapshots()
	if err != nil {
		log.Printf("restore node route snapshots: %v", err)
		return
	}
	if len(snapshots) == 0 {
		return
	}
	nodes, err := h.repo.ListMonitorNodes()
	if err != nil {
		log.Printf("restore node route targets: %v", err)
		return
	}
	byID := make(map[int64]model.Node)
	for _, node := range nodes {
		byID[node.ID] = node
	}
	pool := h.latencyPool()
	loaded := 0
	for _, snapshot := range snapshots {
		var saved savedNodeRoute
		if err := json.Unmarshal([]byte(snapshot.Data), &saved); err != nil || saved.Version != 1 ||
			saved.Result.CheckedAt <= 0 || len(saved.Result.Points) != len(saved.Targets) {
			log.Printf("ignore invalid node route snapshot node=%d", snapshot.NodeID)
			continue
		}
		node, exists := byID[snapshot.NodeID]
		if !exists || saved.Identity != routeNodeIdentity(node) {
			continue
		}
		r := saved.Result
		r.agent, r.identity, r.failed = saved.Agent, saved.Identity, saved.Failed
		byTarget := make(map[routeTargetKey]routePoint)
		for i, p := range r.Points {
			t := saved.Targets[i]
			p.host, p.port, p.asnIncomplete = t.Host, t.Port, t.ASNIncomplete
			if p.IPv6 != nil {
				p.IPv6.host, p.IPv6.port, p.IPv6.asnIncomplete = t.Host, t.Port, t.IPv6ASNIncomplete
			}
			byTarget[routeTargetKey{t.Host, t.Port}] = p
		}
		r.Points = nil
		for _, t := range targetsForNode(pool, node.LatencyTargets) {
			if p, ok := byTarget[routeTargetKey{t.Host, t.Port}]; ok {
				p.Name = t.Name
				if p.IPv6 != nil {
					p.IPv6.Name = t.Name
				}
				r.Points = append(r.Points, p)
			}
		}
		if len(r.Points) > 0 {
			h.nodeRoutes.set(snapshot.NodeID, r)
			loaded++
		}
	}
	if loaded > 0 {
		log.Printf("restored node route snapshots count=%d", loaded)
	}
}
