package contract_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/auth"
)

func TestInboundRelayIPv6EntryIPv4TargetContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)
	admin, _ := auth.GenerateToken(1, "admin_user", 0, secret)
	landing := seedKernelNode(t, r, "landing")
	entry := seedKernelNode(t, r, "entry")
	if err := r.DB().Exec(`UPDATE node SET server_ip = '198.51.100.7', server_ip_v4 = '198.51.100.7', server_ip_v6 = '2001:db8::7' WHERE id = ?`, entry).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Exec(`UPDATE node SET server_ip = '2001:db8::10', server_ip_v6 = '2001:db8::10' WHERE id = ?`, landing).Error; err != nil {
		t.Fatal(err)
	}
	inboundID := seedInboundOnNode(t, router, r, admin, landing,
		`{"nodeId":`+jsonInt64(landing)+`,"kernelType":"xray","name":"landing","protocol":"vless","listen":"::","port":443,"security":{"type":"reality"}}`)
	tunnelID, _ := seedRelayForward(t, r, 1, entry, 0, 0, 0)

	server := httptest.NewServer(router)
	defer server.Close()
	var mu sync.Mutex
	var services []map[string]interface{}
	rejectNewBind := false
	stop := startMockNodeSessionWithCommandRecorder(t, server.URL, "entry-secret", func(cmd string, data json.RawMessage) (bool, string) {
		if cmd == "UpdateService" || cmd == "AddService" {
			var configs []map[string]interface{}
			if err := json.Unmarshal(data, &configs); err != nil {
				return true, err.Error()
			}
			mu.Lock()
			services = append(services, configs...)
			reject := rejectNewBind
			mu.Unlock()
			for _, config := range configs {
				if reject && config["addr"] == "[2001:db8::7]:30002" {
					return true, "mock rejected new listener"
				}
			}
		}
		return false, ""
	})
	defer stop()

	out := kernelContractPost(t, router, admin, "/api/v1/inbound/relay/create",
		`{"inboundId":`+jsonInt64(inboundID)+`,"tunnelId":`+jsonInt64(tunnelID)+`,"inPort":30002,"inIp":"[2001:db8::7]","targetIpFamily":"ipv4"}`)
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	forwardID := int64(out.Data.(map[string]interface{})["forwardId"].(float64))
	if got := mustQueryString(t, r, `SELECT in_ip FROM forward_port WHERE forward_id = ?`, forwardID); got != "2001:db8::7" {
		t.Fatalf("IPv6 bind not saved: %q", got)
	}
	if got := mustQueryString(t, r, `SELECT remote_addr FROM forward WHERE id = ?`, forwardID); got != "203.0.113.10:443" {
		t.Fatalf("IPv4 target not saved: %q", got)
	}
	if got := mustQueryString(t, r, `SELECT target_ip_family FROM node_inbound_relay WHERE forward_id = ?`, forwardID); got != "ipv4" {
		t.Fatalf("target family not saved: %q", got)
	}
	mu.Lock()
	matched := map[string]bool{}
	for _, service := range services {
		if service["addr"] != "[2001:db8::7]:30002" {
			continue
		}
		targets := service["forwarder"].(map[string]interface{})["nodes"].([]interface{})
		if targets[0].(map[string]interface{})["addr"] != "203.0.113.10:443" {
			t.Errorf("wrong dispatched target: %v", targets)
		}
		matched[service["listener"].(map[string]interface{})["type"].(string)] = true
	}
	mu.Unlock()
	if !matched["tcp"] || !matched["udp"] {
		t.Fatalf("TCP and UDP must both bind IPv6: %v", matched)
	}
	share := kernelContractPost(t, router, admin, "/api/v1/inbound/share", `{"id":`+jsonInt64(inboundID)+`}`)
	var relay map[string]interface{}
	for _, item := range share.Data.([]interface{}) {
		view := item.(map[string]interface{})
		if view["kind"] == "relay" {
			relay = view
		}
	}
	if relay == nil || relay["host"] != "2001:db8::7" || !strings.Contains(relay["uri"].(string), "@[2001:db8::7]:30002?") {
		t.Fatalf("share must follow IPv6 listener: %v", relay)
	}

	relayID := int64(out.Data.(map[string]interface{})["id"].(float64))
	update := func(fields string) int {
		t.Helper()
		res := kernelContractPost(t, router, admin, "/api/v1/inbound/relay/update",
			`{"id":`+jsonInt64(relayID)+`,"name":"edited","shareHost":"",`+fields+`}`)
		return res.Code
	}
	if code := update(`"inIp":"198.51.100.7","targetIpFamily":"ipv6"`); code != 0 {
		t.Fatalf("edit address fields: code %d", code)
	}
	if got := mustQueryString(t, r, `SELECT in_ip FROM forward_port WHERE forward_id = ?`, forwardID); got != "198.51.100.7" {
		t.Fatalf("edited IPv4 bind: %q", got)
	}
	if got := mustQueryString(t, r, `SELECT remote_addr FROM forward WHERE id = ?`, forwardID); got != "[2001:db8::10]:443" {
		t.Fatalf("edited IPv6 target: %q", got)
	}
	mu.Lock()
	matched = map[string]bool{}
	for _, service := range services {
		if service["addr"] == "198.51.100.7:30002" {
			targets := service["forwarder"].(map[string]interface{})["nodes"].([]interface{})
			if targets[0].(map[string]interface{})["addr"] == "[2001:db8::10]:443" {
				matched[service["listener"].(map[string]interface{})["type"].(string)] = true
			}
		}
	}
	beforeMetadata := len(services)
	mu.Unlock()
	if !matched["tcp"] || !matched["udp"] {
		t.Fatalf("edit did not update both running services: %v", matched)
	}
	// Older edit forms omit address fields and must preserve explicit settings.
	res := kernelContractPost(t, router, admin, "/api/v1/inbound/relay/update",
		`{"id":`+jsonInt64(relayID)+`,"name":"renamed","shareHost":"relay.example.com"}`)
	if res.Code != 0 {
		t.Fatalf("metadata-only edit: %+v", res)
	}
	mu.Lock()
	afterMetadata := len(services)
	mu.Unlock()
	if afterMetadata != beforeMetadata {
		t.Fatal("renaming must not restart forward services")
	}
	if mustQueryString(t, r, `SELECT target_ip_family FROM node_inbound_relay WHERE id = ?`, relayID) != "ipv6" ||
		mustQueryString(t, r, `SELECT in_ip FROM forward_port WHERE forward_id = ?`, forwardID) != "198.51.100.7" {
		t.Fatal("metadata-only edit reset the saved addresses")
	}
	list := kernelContractPost(t, router, admin, "/api/v1/inbound/relay/list", `{"inboundId":`+jsonInt64(inboundID)+`}`)
	listed := list.Data.([]interface{})[0].(map[string]interface{})
	if listed["inIp"] != "198.51.100.7" || listed["targetIpFamily"] != "ipv6" {
		t.Fatalf("edit form cannot restore the configured addresses: %v", listed)
	}
	for _, fields := range []string{`"inIp":"2001:db8::999"`, `"targetIpFamily":"invalid"`} {
		if code := update(fields); code == 0 {
			t.Fatalf("invalid edit succeeded: %s", fields)
		}
	}
	if mustQueryString(t, r, `SELECT name FROM node_inbound_relay WHERE id = ?`, relayID) != "renamed" {
		t.Fatal("rejected address edit changed relay metadata")
	}
	if code := update(`"inIp":"","targetIpFamily":"auto"`); code != 0 {
		t.Fatalf("reset to defaults: code %d", code)
	}
	if mustQueryString(t, r, `SELECT COALESCE(in_ip, '') FROM forward_port WHERE forward_id = ?`, forwardID) != "" ||
		mustQueryString(t, r, `SELECT target_ip_family FROM node_inbound_relay WHERE id = ?`, relayID) != "auto" {
		t.Fatal("default address selection was not restored")
	}
	// A rejected live update restores both the database and the old services.
	mu.Lock()
	rejectNewBind = true
	mu.Unlock()
	if code := update(`"inIp":"2001:db8::7","targetIpFamily":"ipv4"`); code == 0 {
		t.Fatal("failed node dispatch must fail the edit")
	}
	if mustQueryString(t, r, `SELECT COALESCE(in_ip, '') FROM forward_port WHERE forward_id = ?`, forwardID) != "" ||
		mustQueryString(t, r, `SELECT remote_addr FROM forward WHERE id = ?`, forwardID) != "[2001:db8::10]:443" ||
		mustQueryString(t, r, `SELECT target_ip_family FROM node_inbound_relay WHERE id = ?`, relayID) != "auto" {
		t.Fatal("failed dispatch left partially changed relay addresses")
	}

	// Older clients omit both fields and retain the default entry and target.
	out = kernelContractPost(t, router, admin, "/api/v1/inbound/relay/create",
		`{"inboundId":`+jsonInt64(inboundID)+`,"tunnelId":`+jsonInt64(tunnelID)+`,"inPort":30003}`)
	if out.Code != 0 {
		t.Fatalf("legacy create: %+v", out)
	}
	legacyID := int64(out.Data.(map[string]interface{})["forwardId"].(float64))
	if got := mustQueryString(t, r, `SELECT COALESCE(in_ip, '') FROM forward_port WHERE forward_id = ?`, legacyID); got != "" {
		t.Fatalf("legacy bind changed: %q", got)
	}
	if got := mustQueryString(t, r, `SELECT remote_addr FROM forward WHERE id = ?`, legacyID); got != "[2001:db8::10]:443" {
		t.Fatalf("legacy target changed: %q", got)
	}
}

func TestInboundRelayAddressValidationContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)
	admin, _ := auth.GenerateToken(1, "admin_user", 0, secret)
	landing := seedKernelNode(t, r, "landing")
	entry := seedKernelNode(t, r, "entry")
	inboundID := seedInboundOnNode(t, router, r, admin, landing,
		`{"nodeId":`+jsonInt64(landing)+`,"kernelType":"xray","name":"landing","protocol":"vless","listen":"::","port":443,"security":{"type":"reality"}}`)
	tunnelID, _ := seedRelayForward(t, r, 1, entry, 0, 0, 0)
	for _, tc := range []struct{ fields, want string }{
		{`"inIp":"2001:db8::999"`, "不属于入口服务器"},
		{`"inIp":"0.0.0.0"`, "有效 IPv4 或 IPv6"},
		{`"inIp":"entry.example.com"`, "有效 IPv4 或 IPv6"},
		{`"targetIpFamily":"ipv6"`, "没有可用的 IPv6"},
		{`"targetIpFamily":"invalid"`, "类型无效"},
	} {
		out := kernelContractPost(t, router, admin, "/api/v1/inbound/relay/create",
			`{"inboundId":`+jsonInt64(inboundID)+`,"tunnelId":`+jsonInt64(tunnelID)+`,`+tc.fields+`}`)
		if out.Code == 0 || !strings.Contains(out.Msg, tc.want) {
			t.Fatalf("%s: want %q, got %+v", tc.fields, tc.want, out)
		}
	}
	if mustQueryInt(t, r, `SELECT COUNT(*) FROM forward`) != 0 || mustQueryInt(t, r, `SELECT COUNT(*) FROM node_inbound_relay`) != 0 {
		t.Fatal("invalid addresses must not leave relay or forward records behind")
	}
}
