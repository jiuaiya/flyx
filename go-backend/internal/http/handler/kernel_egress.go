package handler

import (
	"fmt"
	"go-backend/internal/kernel"
)

func validateOutboundIPVersion(kernelType, version, strategy string) error {
	policy, err := kernel.NormalizeOutboundIPStrategy(kernelType, strategy)
	if err != nil {
		return err
	}
	if policy == kernel.OutboundIPv4Only && kernelType == kernel.TypeSingbox && versionBelow(version, 1, 12) {
		return fmt.Errorf("仅 IPv4 出站需要 sing-box 1.12 及以上，节点安装的是 %s", version)
	}
	return nil
}
