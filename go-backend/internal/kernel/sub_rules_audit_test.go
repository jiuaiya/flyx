package kernel

import (
	"slices"
	"strings"
	"testing"
)

func TestSurgeSplitDomainRuleSets(t *testing.T) {
	for _, template := range []bool{false, true} {
		opts := SubOptions{Rules: true, RuleGroups: allRuleGroupKeys()}
		if template {
			b := builderFixture()
			b.Rules = nil
			for _, c := range TemplateRuleCategories() {
				b.Rules = append(b.Rules, TemplateBuilderRule{Type: "category", Value: c.Key, Target: c.Target})
			}
			content, err := BuildSubscriptionTemplate(TemplateSurge, b)
			if err != nil {
				t.Fatal(err)
			}
			opts.Templates = map[string]string{TemplateSurge: content}
		}
		body, _, err := Subscription(FormatSurge, namedEndpoints("美国 01"), opts)
		if err != nil {
			t.Fatal(err)
		}
		rules := iniSection(body, "[Rule]")
		for _, name := range []string{"Advertising", "Apple", "China", "Global"} {
			// These upstream .list files omit their DOMAIN/DOMAIN-SUFFIX
			// entries. Both files must route to the same policy, in place.
			url := "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/" + name + "/" + name
			at := slices.IndexFunc(rules, func(r string) bool { return strings.HasPrefix(r, "RULE-SET,"+url+".list,") })
			if at < 1 {
				t.Fatalf("missing %s rules (template=%v): %s", name, template, body)
			}
			target := strings.TrimPrefix(rules[at], "RULE-SET,"+url+".list,")
			if rules[at-1] != "DOMAIN-SET,"+url+"_Domain.list,"+target {
				t.Fatalf("missing domain half of %s (template=%v): %v", name, template, rules)
			}
		}
		if strings.Count(body, "DOMAIN-SET,") != 4 {
			t.Fatalf("unexpected split rules: %s", body)
		}
		if rules[len(rules)-1] != "FINAL,漏网之鱼,dns-failed" {
			t.Fatal("final rule moved ahead of domain sets")
		}
	}
}

func TestDomesticIPRemoteListsResolve(t *testing.T) {
	loon, _, err := Subscription(FormatLoon, namedEndpoints("美国 01"), SubOptions{Rules: true})
	if err != nil {
		t.Fatal(err)
	}
	remote := iniSection(loon, "[Remote Rule]")
	if last := remote[len(remote)-1]; !strings.Contains(last, "/ChinaIPs/ChinaIPs_Resolve.list,policy=全球直连") {
		t.Fatalf("Loon fallback skips DNS: %s", last)
	}
	b := builderFixture()
	b.Rules = []TemplateBuilderRule{{Type: "category", Value: "cn", Target: "DIRECT"}}
	for _, independent := range []bool{false, true} {
		b.ServiceGroups = &independent
		content, err := BuildSubscriptionTemplate(TemplateSurge, b)
		if err != nil {
			t.Fatal(err)
		}
		target := "DIRECT"
		if independent {
			target = "国内服务"
		}
		want := "RULE-SET,https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/ChinaIPs/ChinaIPs_Resolve.list," + target
		rules := iniSection(content, "[Rule]")
		if rules[len(rules)-2] != want {
			t.Fatalf("Surge template fallback skips DNS: %v", rules)
		}
	}
	// Quantumult X's normal list already resolves; no _Resolve variant exists.
	qx, _, err := Subscription(FormatQX, namedEndpoints("美国 01"), SubOptions{Rules: true})
	if err != nil || !strings.Contains(qx, "/ChinaIPs/ChinaIPs.list,") || strings.Contains(qx, "_Resolve") {
		t.Fatalf("Quantumult X fallback: %v\n%s", err, qx)
	}
}

func TestTemplateBuilderWithoutNodeGroups(t *testing.T) {
	b := builderFixture()
	b.Groups, b.Rules = nil, nil
	for _, kind := range TemplateFormats {
		content, err := BuildSubscriptionTemplate(kind, b)
		if err != nil {
			t.Fatal(err)
		}
		formats := []string{kind}
		if kind == TemplateClash {
			formats = append(formats, FormatStash, FormatShadowrocket)
		}
		for _, format := range formats {
			t.Run(format, func(t *testing.T) {
				for _, names := range [][]string{{"美国 01", "香港 01"}, nil} {
					body, _, err := Subscription(format, namedEndpoints(names...), SubOptions{Templates: map[string]string{format: content}})
					if err != nil {
						t.Fatal(err)
					}
					summary, err := CheckTemplate(kind, body, names)
					if err != nil {
						t.Fatal(err)
					}
					main := summary.Groups[0]
					members := append(append([]string{}, main.Proxies...), main.Nodes...)
					if main.Name != builderMain {
						t.Fatal("main selector missing")
					}
					if len(names) > 0 {
						if len(members) == 0 || !slices.Contains(names, members[0]) {
							t.Fatalf("%s defaults to direct with usable nodes: %+v", format, main)
						}
					} else if format != FormatClash && (len(members) == 0 || (members[0] != "DIRECT" && members[0] != "direct")) {
						t.Fatalf("empty %s selector lacks a fallback: %+v", format, main)
					}
				}
			})
		}
	}
}

func TestSurgeTemplateEmptyAutoGroups(t *testing.T) {
	tpl := `[Proxy]
HK static = ss, 203.0.113.2, 8388, encrypt-method=aes-256-gcm, password=pw

[Proxy Group]
All = select, include-all-proxies=1
Empty = url-test, include-all-proxies=1, policy-regex-filter=NEVERMATCH, url=https://example.com, interval=300
Static = select, include-all-proxies=1, policy-regex-filter=static
Explicit = select, DIRECT, include-all-proxies=1, policy-regex-filter=NEVERMATCH
Remote = select, include-all-proxies=1, policy-path=https://example.com/proxies, policy-regex-filter=NEVERMATCH
Other = select, include-all-proxies=1, include-other-group=All, policy-regex-filter=NEVERMATCH

[Rule]
FINAL,All
`
	body, _, err := Subscription(FormatSurge, namedEndpoints("US 01"), SubOptions{Templates: map[string]string{TemplateSurge: tpl}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Empty = url-test, DIRECT, url=https://example.com, interval=300\n") {
		t.Fatalf("empty filtered group was not repaired: %s", body)
	}
	for _, line := range iniSection(tpl, "[Proxy Group]") {
		if !strings.HasPrefix(line, "Empty =") && !strings.Contains(body, line+"\n") {
			t.Fatalf("nonempty or externally populated group was changed: %s", line)
		}
	}
}
