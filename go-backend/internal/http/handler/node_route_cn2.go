package handler

import "strings"

const routeCN2Uncertain = "电信 CN2（GIA/GT 未确定）"

// Generic AS4134 includes overseas handoffs and provincial access. It does
// not identify the 163 backbone and cannot establish a GT path.
func classifyTelecomRoute(hops []routeHop, reached bool) string {
	cn2 := make(map[string]bool)
	hasCN2, has163, hasCTG := false, false, false
	for _, hop := range hops {
		switch hop.Net {
		case "CTGNet":
			hasCTG = true
		case "163":
			has163 = true
		case "CN2":
			hasCN2 = true
			if ip := canonicalHopIP(hop.IP); ip != "" {
				cn2[ip] = true
			}
		}
	}
	switch {
	case hasCTG:
		return "电信 CTGNet"
	case hasCN2 && has163:
		return "电信 CN2 GT"
	case hasCN2 && reached && len(cn2) >= 2:
		return "电信 CN2 GIA"
	case hasCN2:
		return routeCN2Uncertain
	default:
		return "电信 163"
	}
}

// Missing ASN answers on unrelated hops must not preserve a known-bad GT
// grade when the visible CN2 backbone evidence has already been corrected.
func sameCN2BackboneRoute(a, b string) bool {
	normalize := strings.NewReplacer(routeCN2Uncertain, "电信 CN2", "电信 CN2 GIA", "电信 CN2", "电信 CN2 GT", "电信 CN2")
	return strings.Contains(a, "电信 CN2") && strings.Contains(b, "电信 CN2") && normalize.Replace(a) == normalize.Replace(b)
}

// Reclassify saved observations on startup without fabricating a new probe
// time. A retained grade without its original evidence stays explicitly unsure.
func refreshCachedCN2(p routePoint) (routePoint, bool) {
	old := p
	if strings.Contains(p.Route, "电信 CN2") {
		if p.Stale || p.Error != "" || len(p.Hops) == 0 {
			p.Route = strings.NewReplacer("电信 CN2 GIA", routeCN2Uncertain, "电信 CN2 GT", routeCN2Uncertain).Replace(p.Route)
			p.Stale = true
			p.Detail = "已保留 CN2 识别，现有跳点不足以区分 GIA / GT，等待重新检测"
		} else {
			p.Route = classifyRoute(p.Hops, p.Reached)
			p.Detail = routeDetail(p)
		}
	}
	changed := p.Route != old.Route || p.Detail != old.Detail || p.Stale != old.Stale
	if p.IPv6 != nil {
		leg, legChanged := refreshCachedCN2(*p.IPv6)
		p.IPv6 = &leg
		changed = changed || legChanged
	}
	return p, changed
}
