package kernel

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const (
	OutboundIPAuto   = "auto"
	OutboundIPv4Only = "ipv4_only"
	ipv4ResolverTag  = "flvx-ipv4-resolver"
)

// NormalizeOutboundIPStrategy keeps old configurations automatic and rejects
// unsupported policies instead of silently falling back to a dual-stack exit.
func NormalizeOutboundIPStrategy(kernelType, strategy string) (string, error) {
	strategy = strings.TrimSpace(strategy)
	if strategy == "" {
		strategy = OutboundIPAuto
	}
	if strategy != OutboundIPAuto && strategy != OutboundIPv4Only {
		return "", fmt.Errorf("未知出站 IP 策略 %q", strategy)
	}
	if strategy != OutboundIPAuto && kernelType != TypeXray && kernelType != TypeSingbox {
		return "", fmt.Errorf("仅 IPv4 出站需要 Xray 或 sing-box 内核")
	}
	return strategy, nil
}

// Extra JSON may customize an inbound, but changing its routing identity or
// detouring into another inbound would bypass an inbound-scoped IP policy.
func ValidateOutboundIPExtra(kernelType, strategy string, extra json.RawMessage) error {
	policy, err := NormalizeOutboundIPStrategy(kernelType, strategy)
	if err != nil {
		return err
	}
	if policy != OutboundIPv4Only || len(extra) == 0 {
		return nil
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(extra, &fields); err != nil {
		return fmt.Errorf("高级配置必须是 JSON 对象")
	}
	for _, key := range []string{"tag", "detour"} {
		if _, exists := fields[key]; exists {
			return fmt.Errorf("仅 IPv4 出站时，高级配置不能覆盖 %s 字段", key)
		}
	}
	return nil
}

// applyOutboundIPPolicies runs after chain/split/user rules have been built.
// Only scoped direct outbounds are cloned; their user accounting marks remain
// intact and automatic inbounds keep using the original shared outbounds.
func applyOutboundIPPolicies(kernelType string, cfg obj, inbounds []Inbound) error {
	var restricted []Inbound
	for _, in := range inbounds {
		strategy, err := NormalizeOutboundIPStrategy(kernelType, in.OutboundIPStrategy)
		if err != nil {
			return err
		}
		if strategy == OutboundIPv4Only {
			restricted = append(restricted, in)
		}
	}
	if len(restricted) == 0 {
		return nil
	}
	const v4Bind = "0.0.0.0"
	routeKey, inboundKey, outboundKey, userKey := "route", "inbound", "outbound", "auth_user"
	if kernelType == TypeXray {
		routeKey, inboundKey, outboundKey, userKey = "routing", "inboundTag", "outboundTag", "user"
	}
	route, _ := cfg[routeKey].(obj)
	if route == nil {
		route = obj{}
		cfg[routeKey] = route
	}
	rules, _ := route["rules"].([]interface{})
	outbounds := cfg["outbounds"].([]interface{})
	byTag := map[string]obj{}
	for _, value := range outbounds {
		ob := value.(obj)
		if tag, ok := ob["tag"].(string); ok {
			byTag[tag] = ob
		}
	}
	var prefix, fallback []interface{}
	for _, in := range restricted {
		tags := singboxInboundTags(in)
		if kernelType == TypeXray {
			tags = []string{in.Tag()}
			prefix = append(prefix, obj{"type": "field", inboundKey: tags, "ip": []string{"::/0"}, outboundKey: "block"})
		} else {
			prefix = append(prefix, obj{inboundKey: tags, "ip_version": 6, "action": "reject"})
		}
		patch := func(ob obj) error {
			if hasIPv6Server(ob) {
				return fmt.Errorf("入站 %d 设置仅 IPv4 出站，无法连接 IPv6 地址的出站节点", in.ID)
			}
			if kernelType == TypeXray {
				stream, _ := ob["streamSettings"].(obj)
				if stream == nil {
					stream = obj{}
					ob["streamSettings"] = stream
				}
				sockopt, _ := stream["sockopt"].(obj)
				if sockopt == nil {
					sockopt = obj{}
					stream["sockopt"] = sockopt
				}
				sockopt["domainStrategy"] = "ForceIPv4"
				ob["sendThrough"] = v4Bind
			} else if ob["detour"] == nil {
				ob["domain_resolver"] = obj{"server": ipv4ResolverTag, "strategy": "ipv4_only"}
				ob["inet4_bind_address"] = v4Bind
			}
			return nil
		}
		// Chain/split proxy outbounds are already unique to this inbound.
		for tag, ob := range byTag {
			if tag == chainTag(in) || strings.HasPrefix(tag, chainTag(in)+"-") {
				if err := patch(ob); err != nil {
					return err
				}
			}
		}
		clones := map[string]string{}
		directTag := func(sourceTag string) string {
			if tag := clones[sourceTag]; tag != "" {
				return tag
			}
			original := byTag[sourceTag]
			if original == nil {
				return sourceTag
			}
			if original["type"] != "direct" && original["protocol"] != "freedom" {
				return sourceTag
			}
			// Round-trip through JSON before modifying nested sockopt maps.
			data, _ := json.Marshal(original)
			var copy obj
			_ = json.Unmarshal(data, &copy)
			tag := "ipv4-" + in.Tag() + "-" + sourceTag
			copy["tag"] = tag
			_ = patch(copy) // A direct outbound contains no upstream server.
			outbounds = append(outbounds, copy)
			clones[sourceTag] = tag
			return tag
		}
		for _, value := range rules {
			rule := value.(obj)
			if containsInboundTag(rule[inboundKey], tags) {
				if tag, ok := rule[outboundKey].(string); ok {
					rule[outboundKey] = directTag(tag)
				}
			}
		}
		if in.Via == nil {
			for _, id := range in.countedUsers() {
				user := interface{}([]string{User{ID: id}.Name()})
				if kernelType == TypeXray {
					user = xrayUserMatch(id)
				}
				rule := obj{inboundKey: tags, userKey: user, outboundKey: directTag(userOutboundTag(id))}
				if kernelType == TypeXray {
					rule["type"] = "field"
				}
				fallback = append(fallback, rule)
			}
			rule := obj{inboundKey: tags, outboundKey: directTag("direct")}
			if kernelType == TypeXray {
				rule["type"] = "field"
			}
			fallback = append(fallback, rule)
		}
	}
	// User-wide rules must stay after the new inbound-scoped fallbacks.
	var preprocess, scoped, shared []interface{}
	for _, value := range rules {
		rule := value.(obj)
		if rule["action"] == "sniff" {
			preprocess = append(preprocess, value)
		} else if rule[inboundKey] == nil {
			shared = append(shared, value)
		} else {
			scoped = append(scoped, value)
		}
	}
	route["rules"] = append(append(append(append(prefix, preprocess...), scoped...), fallback...), shared...)
	if kernelType == TypeSingbox {
		route["final"] = "direct"
		cfg["dns"] = obj{"servers": []interface{}{obj{"type": "local", "tag": ipv4ResolverTag}}}
	}
	cfg["outbounds"] = outbounds
	return nil
}

func containsInboundTag(value interface{}, tags []string) bool {
	values, _ := value.([]string)
	for _, candidate := range values {
		for _, tag := range tags {
			if candidate == tag {
				return true
			}
		}
	}
	return false
}

// Only server address fields are checked, not IPv6 client/listen addresses.
func hasIPv6Server(value interface{}) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if key == "server" || key == "address" {
				if address, ok := child.(string); ok {
					if ip := net.ParseIP(strings.Trim(address, "[]")); ip != nil && ip.To4() == nil {
						return true
					}
				}
			}
			if hasIPv6Server(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range v {
			if hasIPv6Server(child) {
				return true
			}
		}
	}
	return false
}
