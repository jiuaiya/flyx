package handler

import (
	"errors"
	"strings"
	"time"

	"go-backend/internal/store/model"
)

func (h *Handler) saveRelayUpdate(rel *model.NodeInboundRelay, name, shareHost string, inIP, targetIPFamily *string) ([]string, error) {
	family := rel.TargetIPFamily
	if targetIPFamily != nil {
		family = strings.TrimSpace(*targetIPFamily)
		switch family {
		case "", "auto", "ipv4", "ipv6":
		default:
			return nil, errors.New("连接落地地址类型无效")
		}
	}
	familyChanged := firstNonEmpty(family, "auto") != firstNonEmpty(rel.TargetIPFamily, "auto")
	saveMetadata := func() error { return h.repo.UpdateInboundRelay(rel.ID, name, shareHost, family) }
	if inIP == nil && !familyChanged {
		return nil, saveMetadata()
	}
	forward, err := h.getForwardRecord(rel.ForwardID)
	if err != nil || forward == nil {
		return nil, errors.New("关联转发不存在，无法修改连接地址")
	}
	ports, err := h.listForwardPorts(forward.ID)
	if err != nil {
		return nil, err
	}
	if len(ports) == 0 {
		return nil, errors.New("转发入口端口不存在")
	}
	bindIP := ""
	bindChanged := false
	if inIP != nil {
		bindIP = strings.Trim(strings.TrimSpace(*inIP), "[]")
		for _, port := range ports {
			if strings.TrimSpace(port.InIP) != bindIP {
				bindChanged = true
			}
		}
	}
	if !familyChanged && !bindChanged {
		return nil, saveMetadata()
	}
	tunnel, err := h.getTunnelRecord(forward.TunnelID)
	if err != nil || tunnel == nil {
		return nil, errors.New("隧道不存在")
	}
	if tunnel.Status != 1 {
		return nil, errors.New("隧道已禁用，无法修改连接地址")
	}
	nft, entryNodes, err := h.tunnelUsesNftables(tunnel.ID)
	if err != nil {
		return nil, err
	}
	if bindChanged {
		bindIP, err = h.relayBindIP(entryNodes, bindIP, nft)
		if err != nil {
			return nil, err
		}
	}
	target := forward.RemoteAddr
	if familyChanged {
		inbound, err := h.repo.GetNodeInbound(rel.InboundID)
		if err != nil || inbound == nil {
			return nil, errors.New("入站不存在")
		}
		target, err = h.relayTargetForFamily(inbound, tunnel, family)
		if err != nil {
			return nil, err
		}
	}
	if nft {
		if err := h.validateNftablesForwardRequest(tunnel, target, entryNodes); err != nil {
			return nil, err
		}
	}
	if !bindChanged && target == forward.RemoteAddr {
		return nil, saveMetadata()
	}
	if err := h.repo.UpdateForward(forward.ID, forward.Name, forward.TunnelID, target, forward.Strategy, time.Now().UnixMilli(),
		forward.SpeedID, forward.MaxConn, forward.IPMaxConn, forward.IPSpeedID, forward.ProxyProtocol, forward.ProxyProtocolReceive, forward.ProxyProtocolSend); err != nil {
		return nil, err
	}
	rollback := func(err error) ([]string, error) {
		h.rollbackForwardMutation(forward, ports)
		return nil, err
	}
	if bindChanged {
		updatedPorts := append([]forwardPortRecord(nil), ports...)
		for i := range updatedPorts {
			updatedPorts[i].InIP = bindIP
		}
		if err := h.replaceForwardPortsWithRecords(forward.ID, updatedPorts); err != nil {
			return rollback(err)
		}
	}
	updated, err := h.getForwardRecord(forward.ID)
	if err != nil {
		return rollback(err)
	}
	warnings, err := h.syncForwardServicesWithWarnings(updated, "UpdateService", true)
	if err != nil {
		return rollback(err)
	}
	if err := saveMetadata(); err != nil {
		return rollback(err)
	}
	return warnings, nil
}
