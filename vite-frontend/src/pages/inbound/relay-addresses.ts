import type { InboundApiItem, NodeApiItem, TunnelApiItem } from "@/api/types";

export type RelayIPFamily = "auto" | "ipv4" | "ipv6";

function host(value: unknown): string {
  return typeof value === "string" ? value.trim().replace(/^\[|\]$/g, "") : "";
}

function ipFamily(value: string): RelayIPFamily | null {
  if (value.includes(":")) return "ipv6";
  if (/^(\d{1,3}\.){3}\d{1,3}$/.test(value)) return "ipv4";

  return null;
}

export function relayNodeIPs(node?: NodeApiItem): string[] {
  if (!node) return [];

  return [
    ...new Set(
      [
        node.serverIpV4,
        node.serverIpV6,
        node.serverIp,
        ...(typeof node.extraIPs === "string" ? node.extraIPs.split(",") : []),
      ]
        .map(host)
        .filter(
          (ip) =>
            ipFamily(ip) &&
            ip !== "0.0.0.0" &&
            ip !== "::" &&
            ip !== "::1" &&
            !ip.startsWith("127."),
        ),
    ),
  ];
}

export function relayIPLabel(ip: string): string {
  return `${ipFamily(ip) === "ipv6" ? "IPv6" : "IPv4"} · ${ip}`;
}

export function relayEntryIPs(
  tunnel: TunnelApiItem | undefined,
  nodes: NodeApiItem[],
): string[] {
  const entries = tunnel?.inNodeId ?? [];

  if (entries.length !== 1 || tunnel?.forwardMode === "nftables") return [];

  return relayNodeIPs(nodes.find((n) => n.id === entries[0].nodeId));
}

/** Literal targets only: DNS lookup belongs to the forwarding server. */
export function relayTargetIPs(
  inbound: InboundApiItem | null,
  tunnel: TunnelApiItem | undefined,
  nodes: NodeApiItem[],
): Partial<Record<RelayIPFamily, string>> {
  if (!inbound || !tunnel) return {};
  const listen = host(inbound.listen);
  const local =
    tunnel.type === 2 &&
    (tunnel.outNodeId ?? []).some((n) => n.nodeId === inbound.nodeId);
  const wildcard = listen === "0.0.0.0" || listen === "::";

  if (local && (wildcard || listen === "::1" || listen.startsWith("127."))) {
    return {
      ...(listen === "::1" ? {} : { ipv4: "127.0.0.1" }),
      ...(ipFamily(listen) === "ipv6" ? { ipv6: "::1" } : {}),
    };
  }
  if (!wildcard) {
    const family = ipFamily(listen);

    return family ? { [family]: listen } : {};
  }
  const node = nodes.find((n) => n.id === inbound.nodeId);
  const addresses = [node?.serverIpV4, node?.serverIpV6, node?.serverIp].map(
    host,
  );

  return {
    ipv4: addresses.find((ip) => ipFamily(ip) === "ipv4"),
    ipv6:
      listen === "::"
        ? addresses.find((ip) => ipFamily(ip) === "ipv6")
        : undefined,
  };
}
