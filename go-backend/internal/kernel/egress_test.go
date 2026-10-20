package kernel

import (
	"reflect"
	"strings"
	"testing"
)

func TestIPv4OnlyMixedInbounds(t *testing.T) {
	for _, kt := range []string{TypeXray, TypeSingbox} {
		t.Run(kt, func(t *testing.T) {
			in := Inbound{ID: 1, Protocol: ProtocolVLESS, Listen: "::", Port: 8388,
				Settings: Settings{UUID: "00000000-0000-4000-8000-000000000000"},
				Users:    chainUser, OutboundIPStrategy: OutboundIPv4Only}
			auto := in
			auto.ID, auto.Port, auto.OutboundIPStrategy = 2, 8389, OutboundIPAuto
			cfg := renderJSON(t, kt, []Inbound{in, auto})
			routeKey, obKey := "route", "outbound"
			if kt == TypeXray {
				routeKey, obKey = "routing", "outboundTag"
			}
			rules := cfg[routeKey].(map[string]interface{})["rules"].([]interface{})
			want := []string{"reject", "ipv4-in-1-flvx-u7", "ipv4-in-1-direct", "flvx-u7"}
			if kt == TypeXray {
				want[0] = "block"
			}
			if got := ruleOutbounds(rules, obKey); !reflect.DeepEqual(got, want) {
				t.Fatalf("scoped rules must precede shared user routing: %v", got)
			}
			first := rules[0].(map[string]interface{})
			if kt == TypeXray {
				if !reflect.DeepEqual(first["ip"], []interface{}{"::/0"}) {
					t.Fatalf("IPv6 destinations must be blocked: %v", first)
				}
			} else if first["ip_version"] != float64(6) {
				t.Fatalf("IPv6 destinations must be rejected: %v", first)
			}
			for _, tag := range []string{"ipv4-in-1-direct", "ipv4-in-1-flvx-u7"} {
				assertIPv4Dialer(t, kt, outboundByTag(cfg, tag), tag == "ipv4-in-1-flvx-u7")
			}
			// The IPv6 listener and the other inbound's shared dialers remain automatic.
			if cfg["inbounds"].([]interface{})[0].(map[string]interface{})["listen"] != "::" {
				t.Fatal("IPv4 egress must preserve the IPv6 listener")
			}
			shared := outboundByTag(cfg, "flvx-u7")
			if shared["domain_resolver"] != nil || shared["sendThrough"] != nil || shared["inet4_bind_address"] != nil {
				t.Fatalf("shared outbound was modified: %v", shared)
			}
			if kt == TypeSingbox {
				dns := cfg["dns"].(map[string]interface{})["servers"].([]interface{})[0].(map[string]interface{})
				if dns["tag"] != ipv4ResolverTag || dns["type"] != "local" {
					t.Fatalf("missing system resolver: %v", dns)
				}
			}
		})
	}
}

func assertIPv4Dialer(t *testing.T, kt string, ob map[string]interface{}, counted bool) {
	t.Helper()
	if ob == nil {
		t.Fatal("missing IPv4 outbound")
	}
	if kt == TypeXray {
		sock := ob["streamSettings"].(map[string]interface{})["sockopt"].(map[string]interface{})
		if ob["sendThrough"] != "0.0.0.0" || sock["domainStrategy"] != "ForceIPv4" {
			t.Fatalf("Xray must fail closed when no A record exists: %v", ob)
		}
		if counted && sock["mark"] != float64(UserMark(7)) {
			t.Fatalf("lost accounting socket mark: %v", ob)
		}
	} else {
		resolver := ob["domain_resolver"].(map[string]interface{})
		if ob["inet4_bind_address"] != "0.0.0.0" || resolver["strategy"] != "ipv4_only" || resolver["server"] != ipv4ResolverTag {
			t.Fatalf("sing-box must resolve/dial IPv4 only: %v", ob)
		}
		if counted && ob["routing_mark"] != float64(UserMark(7)) {
			t.Fatalf("lost accounting socket mark: %v", ob)
		}
	}
}

func TestIPv4OnlySplitAndChain(t *testing.T) {
	for _, kt := range []string{TypeXray, TypeSingbox} {
		t.Run(kt, func(t *testing.T) {
			in := splitInbound()
			in.OutboundIPStrategy = OutboundIPv4Only
			in.Via = &Endpoint{Host: "exit.example.com", Port: 443, Proxy: Proxy{"type": "trojan", "server": "exit.example.com", "port": 443, "password": "x"}}
			cfg := renderJSON(t, kt, []Inbound{in})
			for _, tag := range []string{"via-1", "via-1-u7", "via-1-s1", "via-1-s1-u7", "ipv4-in-1-flvx-u7", "ipv4-in-1-direct"} {
				assertIPv4Dialer(t, kt, outboundByTag(cfg, tag), strings.HasSuffix(tag, "u7"))
			}
			routeKey, obKey := "route", "outbound"
			if kt == TypeXray {
				routeKey, obKey = "routing", "outboundTag"
			}
			rules := cfg[routeKey].(map[string]interface{})["rules"].([]interface{})
			if kt == TypeSingbox && rules[1].(map[string]interface{})["action"] != "sniff" {
				t.Fatal("protocol sniffing must still run before split rules")
			}
			targets := strings.Join(ruleOutbounds(rules, obKey), ",")
			if strings.Contains(targets, ",direct,") || !strings.HasSuffix(targets, "via-1-u7,via-1,flvx-u7") {
				t.Fatalf("split direct must use scoped dialer and default must retain chain: %s", targets)
			}
			in.Via = &Endpoint{Host: "2001:db8::1", Port: 8388, Proxy: Proxy{"type": "ss", "server": "2001:db8::1", "port": 8388, "cipher": "aes-128-gcm", "password": "x"}}
			if _, err := Render(kt, []Inbound{in}); err == nil {
				t.Fatal("an IPv6 next hop must be refused")
			}
		})
	}
}

func TestIPv4OnlyShadowTLSAndValidation(t *testing.T) {
	in := Inbound{ID: 3, Protocol: ProtocolShadowsocks, Port: 443,
		Settings:           Settings{Method: "aes-128-gcm", Password: "x"},
		Security:           Security{Type: SecurityShadowTLS, SNI: "www.example.com", ShadowTLSPassword: "pw"},
		OutboundIPStrategy: OutboundIPv4Only}
	cfg := renderJSON(t, TypeSingbox, []Inbound{in})
	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	if got := rules[0].(map[string]interface{})["inbound"]; !reflect.DeepEqual(got, []interface{}{"in-3", "in-3-ss"}) {
		t.Fatalf("both ShadowTLS and decrypted traffic must be covered: %v", got)
	}
	for _, tt := range []struct{ kernel, policy string }{{TypeSnell, OutboundIPv4Only}, {TypeXray, "ipv4_prefer"}} {
		if _, err := NormalizeOutboundIPStrategy(tt.kernel, tt.policy); err == nil {
			t.Fatalf("unsupported policy accepted: %+v", tt)
		}
	}
	if got, err := NormalizeOutboundIPStrategy(TypeSnell, ""); err != nil || got != OutboundIPAuto {
		t.Fatalf("old configurations must remain automatic: %q %v", got, err)
	}
}
