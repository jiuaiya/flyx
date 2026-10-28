package handler

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
)

// An explicit family is strict: a missing address must not silently switch
// the relay back to the other family when the landing is edited later.
func (h *Handler) relayTargetForFamily(row *model.NodeInbound, tunnel *tunnelRecord, family string) (string, error) {
	family = strings.TrimSpace(family)
	if family != "" && family != "auto" && family != "ipv4" && family != "ipv6" {
		return "", errors.New("连接落地地址类型无效")
	}
	target, err := h.relayTarget(row, tunnel)
	if err != nil || family == "" || family == "auto" {
		return target, err
	}
	v6 := family == "ipv6"
	label := "IPv4"
	if v6 {
		label = "IPv6"
	}
	listen := net.ParseIP(strings.Trim(row.Listen, "[]"))
	if listen != nil && ((!v6 && listen.Equal(net.IPv6loopback)) || (v6 && listen.To4() != nil)) {
		return "", fmt.Errorf("落地入站未监听 %s 地址", label)
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		host = "127.0.0.1"
		if v6 {
			host = "::1"
		}
	} else if isWildcardListen(row.Listen) {
		node, err := h.repo.GetNodeByID(row.NodeID)
		if err != nil {
			return "", err
		}
		host = nodeShareIP(node, v6)
	}
	ip := net.ParseIP(host)
	if ip == nil || (ip.To4() == nil) != v6 {
		return "", fmt.Errorf("落地没有可用的 %s 地址，请检查服务器地址和入站监听配置", label)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(row.Port)), nil
}

func (h *Handler) relayBindIP(entryNodes []int64, inIP string, nft bool) (string, error) {
	inIP = strings.Trim(strings.TrimSpace(inIP), "[]")
	if inIP == "" {
		return "", nil
	}
	if nft {
		return "", errors.New("nftables 转发不支持指定入口监听 IP，请使用面板转发")
	}
	if len(entryNodes) != 1 {
		return "", errors.New("多入口隧道的转发不支持自定义监听IP")
	}
	ip := net.ParseIP(inIP)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return "", errors.New("请选择入口服务器的有效 IPv4 或 IPv6 地址")
	}
	node, err := h.getNodeRecord(entryNodes[0])
	if err != nil {
		return "", err
	}
	candidates := append([]string{node.ServerIP, node.ServerIPv4, node.ServerIPv6}, strings.Split(node.ExtraIPs, ",")...)
	for _, candidate := range candidates {
		if ip.Equal(net.ParseIP(strings.Trim(strings.TrimSpace(candidate), "[]"))) {
			return ip.String(), nil
		}
	}
	return "", errors.New("所选监听 IP 不属于入口服务器，请刷新后重试")
}
