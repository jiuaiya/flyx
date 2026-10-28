import type { InboundApiItem, NodeApiItem, TunnelApiItem } from "@/api/types";

import { Button } from "@/shadcn-bridge/heroui/button";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import {
  relayEntryIPs,
  relayIPLabel,
  relayTargetIPs,
  type RelayIPFamily,
} from "@/pages/inbound/relay-addresses";

export interface RelayAddressValues {
  inIp: string;
  targetIpFamily: RelayIPFamily;
}

/** Shared by create and edit so both paths configure the actual forward. */
export function RelayAddressFields({
  inbound,
  tunnel,
  nodes,
  value,
  onChange,
}: {
  inbound: InboundApiItem | null;
  tunnel?: TunnelApiItem;
  nodes: NodeApiItem[];
  value: RelayAddressValues;
  onChange: (value: RelayAddressValues) => void;
}) {
  if (!tunnel) return null;
  const entryIPs = relayEntryIPs(tunnel, nodes);
  const targetIPs = relayTargetIPs(inbound, tunnel, nodes);
  const ipv6Entry = entryIPs.find((ip) => ip.includes(":"));
  const multiEntry = (tunnel.inNodeId?.length ?? 0) > 1;
  const nftEntry = tunnel.forwardMode === "nftables";
  const hasSavedIP = value.inIp && !entryIPs.includes(value.inIp);

  return (
    <div className="space-y-3 rounded-lg bg-default-50 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm font-medium">连接地址</p>
        {ipv6Entry && targetIPs.ipv4 && (
          <Button
            color="primary"
            size="sm"
            variant="flat"
            onPress={() =>
              onChange({ inIp: ipv6Entry, targetIpFamily: "ipv4" })
            }
          >
            IPv6 入站 → IPv4 落地
          </Button>
        )}
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Select
          description={
            multiEntry
              ? "多入口隧道使用各服务器默认监听地址"
              : nftEntry
                ? "nftables 使用服务器默认转发地址"
                : "客户端连接的入口 IP，订阅自动使用该地址"
          }
          isDisabled={
            multiEntry || nftEntry || (!entryIPs.length && !hasSavedIP)
          }
          label="入口监听 IP"
          selectedKeys={[value.inIp || "__default__"]}
          onSelectionChange={(keys) => {
            const ip = String(Array.from(keys)[0] ?? "__default__");

            onChange({ ...value, inIp: ip === "__default__" ? "" : ip });
          }}
        >
          <SelectItem key="__default__">服务器默认监听</SelectItem>
          {hasSavedIP && (
            <SelectItem key={value.inIp}>当前配置 · {value.inIp}</SelectItem>
          )}
          {entryIPs.map((ip) => (
            <SelectItem key={ip}>{relayIPLabel(ip)}</SelectItem>
          ))}
        </Select>
        <Select
          description="中转或出口服务器连接落地时使用的地址"
          disabledKeys={["ipv4", "ipv6"].filter(
            (family) => !targetIPs[family as RelayIPFamily],
          )}
          label="连接落地地址"
          selectedKeys={[value.targetIpFamily]}
          onSelectionChange={(keys) =>
            onChange({
              ...value,
              targetIpFamily: String(
                Array.from(keys)[0] ?? "auto",
              ) as RelayIPFamily,
            })
          }
        >
          <SelectItem key="auto">自动选择</SelectItem>
          <SelectItem key="ipv4">
            {targetIPs.ipv4 ? `IPv4 · ${targetIPs.ipv4}` : "IPv4（无可用地址）"}
          </SelectItem>
          <SelectItem key="ipv6">
            {targetIPs.ipv6 ? `IPv6 · ${targetIPs.ipv6}` : "IPv6（无可用地址）"}
          </SelectItem>
        </Select>
      </div>
      <p className="break-all text-xs text-default-500">
        {value.inIp ? relayIPLabel(value.inIp) : "默认入口"}
        {" → "}
        {value.targetIpFamily === "auto"
          ? "自动连接落地"
          : `${value.targetIpFamily === "ipv6" ? "IPv6" : "IPv4"} · ${targetIPs[value.targetIpFamily] || "地址不可用"}`}
      </p>
    </div>
  );
}
