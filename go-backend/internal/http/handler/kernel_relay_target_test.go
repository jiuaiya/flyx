package handler

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"go-backend/internal/kernel"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func seedRelayTargetNode(t *testing.T, r *repo.Repository, name, ip string) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
		VALUES(?, ?, ?, ?, '', '30000-30010', '', 'v1', 0, 0, 0, ?, ?, 0, '[::]', '[::]', 0)`, name, name+"-secret", ip, ip, now, now).Error; err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.DB().Raw(`SELECT id FROM node WHERE name = ?`, name).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRelayTargetAddressFamily(t *testing.T) {
	r, err := repo.Open(t.TempDir() + "/relay-family.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := New(r, "unit-test-secret")
	front := seedRelayTargetNode(t, r, "front", "198.51.100.1")
	landing := seedRelayTargetNode(t, r, "landing", "203.0.113.10")
	if err := r.DB().Model(&model.Node{}).Where("id = ?", landing).
		Updates(map[string]interface{}{"server_ip": "2001:db8::10", "server_ip_v6": "2001:db8::10"}).Error; err != nil {
		t.Fatal(err)
	}
	port := &tunnelRecord{Type: 1}
	local := &tunnelRecord{ID: seedRelayTargetTunnel(t, r, "local", front, landing), Type: 2}
	cases := []struct {
		name, listen, family, want, wantErr string
		tunnel                              *tunnelRecord
	}{
		{"automatic retains default", "::", "auto", "[2001:db8::10]:443", "", port},
		{"IPv4 overrides IPv6 default", "::", "ipv4", "203.0.113.10:443", "", port},
		{"IPv6 public", "::", "ipv6", "[2001:db8::10]:443", "", port},
		{"IPv4 wildcard rejects IPv6", "0.0.0.0", "ipv6", "", "未监听 IPv6", port},
		{"fixed IPv4", "203.0.113.20", "ipv4", "203.0.113.20:443", "", port},
		{"fixed IPv6 rejects IPv4", "2001:db8::20", "ipv4", "", "没有可用的 IPv4", port},
		{"domain stays automatic", "landing.example.com", "auto", "landing.example.com:443", "", port},
		{"domain has no literal IPv4", "landing.example.com", "ipv4", "", "没有可用的 IPv4", port},
		{"local IPv4", "::", "ipv4", "127.0.0.1:443", "", local},
		{"local IPv6", "::", "ipv6", "[::1]:443", "", local},
		{"IPv6 loopback rejects IPv4", "::1", "ipv4", "", "未监听 IPv4", local},
		{"IPv4 loopback rejects IPv6", "127.0.0.1", "ipv6", "", "未监听 IPv6", local},
		{"invalid family", "::", "v9", "", "类型无效", port},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := &model.NodeInbound{NodeID: landing, Listen: tc.listen, Port: 443}
			got, err := h.relayTargetForFamily(row, tc.tunnel, tc.family)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %q %v", tc.wantErr, got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q %v, want %q", got, err, tc.want)
			}
		})
	}
	// A saved IPv4 selection survives retargeting and reports a missing
	// family instead of quietly sending the relay over IPv6.
	row := &model.NodeInbound{NodeID: landing, Listen: "::", Port: 443}
	if err := r.DB().Model(&model.Node{}).Where("id = ?", landing).Update("server_ip_v4", "").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := h.relayTargetForFamily(row, port, "ipv4"); err == nil {
		t.Fatal("missing IPv4 must not fall back to IPv6")
	}
	if err := r.DB().Model(&model.Node{}).Where("id = ?", landing).Update("server_ip", "").Error; err != nil {
		t.Fatal(err)
	}
	if got, err := h.relayTargetForFamily(row, port, "ipv6"); err != nil || got != "[2001:db8::10]:443" {
		t.Fatalf("IPv6-only landing: %q %v", got, err)
	}
}

func TestRetargetRelayPreservesAddressFamily(t *testing.T) {
	r, err := repo.Open(t.TempDir() + "/relay-retarget.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := New(r, "unit-test-secret")
	front := seedRelayTargetNode(t, r, "front", "198.51.100.1")
	landing := seedRelayTargetNode(t, r, "landing", "203.0.113.10")
	tunnelID := seedRelayTargetTunnel(t, r, "relay", front)
	if err := r.DB().Model(&model.Tunnel{}).Where("id = ?", tunnelID).Update("type", 1).Error; err != nil {
		t.Fatal(err)
	}
	in := seedChainInbound(t, r, seedChainKernel(t, r, landing, "xray"), "landing", kernel.Inbound{Protocol: kernel.ProtocolShadowsocks, Listen: "::", Port: 443}, 0)
	forwardID, err := r.CreateForwardTx(1, "admin", "relay", tunnelID, "203.0.113.10:443", "fifo", time.Now().UnixMilli(), 0, []int64{front}, 30001, "", nil, 0, 0, nil, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	rel := &model.NodeInboundRelay{InboundID: in.ID, ForwardID: forwardID, Name: "relay", TargetIPFamily: "ipv4", Managed: 1, CreatedTime: time.Now().UnixMilli()}
	if err := r.CreateInboundRelay(rel); err != nil {
		t.Fatal(err)
	}
	node, _ := r.GetNodeByID(landing)
	node.ServerIP = "2001:db8::10"
	node.ServerIPV4 = sql.NullString{String: "203.0.113.20", Valid: true}
	if err := r.DB().Save(node).Error; err != nil {
		t.Fatal(err)
	}
	if problems := h.retargetRelays(in); len(problems) > 0 {
		t.Fatal(problems)
	}
	fwd, err := h.getForwardRecord(forwardID)
	if err != nil || fwd.RemoteAddr != "203.0.113.20:443" {
		t.Fatalf("retarget lost IPv4 selection: %+v %v", fwd, err)
	}
}

func seedRelayTargetTunnel(t *testing.T, r *repo.Repository, name string, entry int64, exits ...int64) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, 1.0, 2, 'tcp', 99999, ?, ?, 1, NULL, 0)`, name, now, now).Error; err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := r.DB().Raw(`SELECT id FROM tunnel WHERE name = ?`, name).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(?, 1, ?, NULL, 'round', 1, 'tcp')`, id, entry).Error; err != nil {
		t.Fatal(err)
	}
	for _, exit := range exits {
		if err := r.DB().Exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(?, 3, ?, 31000, 'round', 1, 'tcp')`, id, exit).Error; err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// A tunnel exiting on another node (front → IX → landing) dials the landing
// node's public address, and the whitelist must admit the exit node.
func TestRelayTargetTunnelThroughIX(t *testing.T) {
	r, err := repo.Open(t.TempDir() + "/relay-target.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := New(r, "unit-test-secret")

	front := seedRelayTargetNode(t, r, "front", "198.51.100.1")
	ix := seedRelayTargetNode(t, r, "ix", "198.51.100.2")
	landing := seedRelayTargetNode(t, r, "landing", "203.0.113.10")

	viaIX := &tunnelRecord{ID: seedRelayTargetTunnel(t, r, "via-ix", front, ix), Type: 2}
	direct := &tunnelRecord{ID: seedRelayTargetTunnel(t, r, "direct", front, landing), Type: 2}
	mixed := &tunnelRecord{ID: seedRelayTargetTunnel(t, r, "mixed", front, landing, ix), Type: 2}

	public := &model.NodeInbound{NodeID: landing, Listen: "::", Port: 443}
	local := &model.NodeInbound{NodeID: landing, Listen: "127.0.0.1", Port: 444}
	imported := &model.NodeInbound{Listen: "198.51.100.9", Port: 8388}

	cases := []struct {
		name    string
		row     *model.NodeInbound
		tunnel  *tunnelRecord
		want    string
		wantErr string
	}{
		{"via IX dials the public address", public, viaIX, "203.0.113.10:443", ""},
		{"exit on landing dials locally", public, direct, "127.0.0.1:443", ""},
		{"loopback inbound via IX", local, viaIX, "", "只监听本机"},
		{"loopback inbound exiting on landing", local, direct, "127.0.0.1:444", ""},
		{"landing mixed with other exits", public, mixed, "", "只能有这一个出口"},
		{"imported node via IX", imported, viaIX, "198.51.100.9:8388", ""},
		{"imported node via any tunnel", imported, direct, "198.51.100.9:8388", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.relayTarget(tc.row, tc.tunnel)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %q %v", tc.wantErr, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q %v, want %q", got, err, tc.want)
			}
		})
	}

	dialers, err := h.relayDialerNodeIDs(viaIX.ID, 2)
	if err != nil || len(dialers) != 1 || dialers[0] != ix {
		t.Fatalf("tunnel forwards reach the landing node from the exit: %v %v", dialers, err)
	}
	dialers, err = h.relayDialerNodeIDs(viaIX.ID, 1)
	if err != nil || len(dialers) != 1 || dialers[0] != front {
		t.Fatalf("port forwards reach the landing node from the entry: %v %v", dialers, err)
	}
}
