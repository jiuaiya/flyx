package kernel

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Domain-only requests (including fake-IP traffic) need a DNS lookup at
// the domestic IP rule. Otherwise unlisted domestic sites hit MATCH.
func TestSubscriptionDomesticIPFallbackClash(t *testing.T) {
	for _, template := range []bool{false, true} {
		formats := []string{FormatClash, FormatStash}
		opts := SubOptions{Rules: true, RuleGroups: []string{"telegram"}}
		if template {
			b := builderFixture()
			b.Rules = []TemplateBuilderRule{
				{Type: "category", Value: "private", Target: "DIRECT"},
				{Type: "category", Value: "telegram", Target: builderMain},
				{Type: "category", Value: "cn", Target: "DIRECT"},
			}
			content, err := BuildSubscriptionTemplate(TemplateClash, b)
			if err != nil {
				t.Fatal(err)
			}
			opts.Templates = map[string]string{TemplateClash: content, TemplateShadowrocket: content}
			formats = append(formats, FormatShadowrocket)
		}
		for _, format := range formats {
			name := format + "/builtin"
			if template {
				name = format + "/template"
			}
			t.Run(name, func(t *testing.T) {
				body, _, err := Subscription(format, namedEndpoints("美国 01"), opts)
				if err != nil {
					t.Fatal(err)
				}
				var cfg struct {
					Rules []string `yaml:"rules"`
				}
				if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
					t.Fatal(err)
				}
				target := ruleDirectGroup
				if template {
					target = "国内服务"
				}
				want := []string{"RULE-SET,site-cn," + target, "RULE-SET,ip-cn," + target, "MATCH," + ruleFinalGroup}
				if !slices.Equal(cfg.Rules[len(cfg.Rules)-3:], want) {
					t.Fatalf("domestic IP lookup must precede final: %v", cfg.Rules)
				}
				for _, tag := range []string{"ip-private", "ip-telegram"} {
					i := slices.IndexFunc(cfg.Rules, func(r string) bool { return strings.HasPrefix(r, "RULE-SET,"+tag+",") })
					if i < 0 || !strings.HasSuffix(cfg.Rules[i], ",no-resolve") {
						t.Fatalf("early IP rule %s should not force DNS: %v", tag, cfg.Rules)
					}
				}
			})
		}
	}
}

func TestSubscriptionDomesticIPFallbackSingbox(t *testing.T) {
	for _, mode := range []string{"builtin", "selector", "direct", "reject"} {
		t.Run(mode, func(t *testing.T) {
			opts := SubOptions{Rules: true}
			wantTarget := ruleDirectGroup
			if mode != "builtin" {
				b := builderFixture()
				b.Rules = []TemplateBuilderRule{
					{Type: "domain_suffix", Value: "custom.example", Target: builderMain},
					{Type: "category", Value: "cn", Target: "DIRECT"},
					{Type: "category", Value: "noncn", Target: builderMain},
				}
				wantTarget = "国内服务"
				if mode == "direct" {
					disabled := false
					b.ServiceGroups = &disabled
					wantTarget = "direct"
				} else if mode == "reject" {
					b.Rules[1].Target = "REJECT"
				}
				content, err := BuildSubscriptionTemplate(TemplateSingbox, b)
				if err != nil {
					t.Fatal(err)
				}
				opts.Templates = map[string]string{TemplateSingbox: content}
			}
			body, _, err := Subscription(FormatSingbox, namedEndpoints("美国 01"), opts)
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Route struct {
					Rules []struct {
						RuleSet  []string `json:"rule_set"`
						Action   string   `json:"action"`
						Outbound string   `json:"outbound"`
					} `json:"rules"`
					Final string `json:"final"`
				} `json:"route"`
			}
			if err := json.Unmarshal([]byte(body), &cfg); err != nil {
				t.Fatal(err)
			}
			cn, resolves := -1, 0
			for i, r := range cfg.Route.Rules {
				if slices.Contains(r.RuleSet, "site-cn") {
					cn = i
				}
				if r.Action == "resolve" {
					resolves++
				}
			}
			if cn < 1 || cn+2 >= len(cfg.Route.Rules) || resolves != 1 {
				t.Fatalf("missing domestic fallback: %s", body)
			}
			site, resolve, ip := cfg.Route.Rules[cn], cfg.Route.Rules[cn+1], cfg.Route.Rules[cn+2]
			if !slices.Equal(site.RuleSet, []string{"site-cn"}) || resolve.Action != "resolve" || !slices.Equal(ip.RuleSet, []string{"ip-cn"}) {
				t.Fatalf("expected domain match, resolve, then IP match: %s", body)
			}
			if mode == "reject" {
				if site.Action != "reject" || ip.Action != "reject" || site.Outbound != "" || ip.Outbound != "" {
					t.Fatalf("reject target lost: %s", body)
				}
			} else if site.Outbound != wantTarget || ip.Outbound != wantTarget {
				t.Fatalf("domestic target changed: %s", body)
			}
			wantFinal := ruleFinalGroup
			if mode == "direct" {
				wantFinal = builderMain
			}
			if cfg.Route.Final != wantFinal {
				t.Fatalf("final target changed: %s", body)
			}
			if mode != "builtin" && !slices.Equal(cfg.Route.Rules[cn+3].RuleSet, []string{"site-geolocation-not-cn"}) {
				t.Fatalf("user rule order changed: %s", body)
			}
		})
	}
}

func TestSubscriptionRulesSurgeGroupReferences(t *testing.T) {
	for _, names := range [][]string{{"美国 01", "香港 01"}, nil} {
		body, _, err := Subscription(FormatSurge, namedEndpoints(names...), SubOptions{Rules: true, RuleGroups: allRuleGroupKeys()})
		if err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{"DIRECT": true, "REJECT": true}
		for _, name := range names {
			known[name] = true
		}
		groups := map[string][]string{}
		for _, line := range iniSection(body, "[Proxy Group]") {
			name, value, ok := strings.Cut(line, " = ")
			if !ok {
				continue
			}
			known[name] = true
			groups[name] = strings.Split(value, ", ")[1:]
		}
		for name, members := range groups {
			for _, member := range members {
				if !strings.Contains(member, "=") && !known[member] {
					t.Errorf("%s references undefined policy %q", name, member)
				}
			}
		}
		if members := groups[ruleFinalGroup]; len(members) == 0 || members[0] != "节点选择" || !slices.Contains(members, "自动选择") {
			t.Fatalf("unexpected final choices: %s", body)
		}
		want := "自动选择 = select, DIRECT"
		if len(names) > 0 {
			want = "自动选择 = url-test, " + strings.Join(names, ", ") + ", url="
		}
		if !strings.Contains(body, want) {
			t.Fatalf("missing automatic selection group: %s", body)
		}
	}
}
