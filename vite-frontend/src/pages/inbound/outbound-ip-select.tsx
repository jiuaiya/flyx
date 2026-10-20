import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

export function OutboundIPSelect({
  value,
  kernelType,
  onChange,
}: {
  value: "auto" | "ipv4_only";
  kernelType: string;
  onChange: (value: "auto" | "ipv4_only") => void;
}) {
  const supported = kernelType === "xray" || kernelType === "singbox";

  return (
    <Select
      description={
        supported
          ? "保留 IPv6 入站；仅 IPv4 时只解析和连接 IPv4，并拒绝 IPv6 目标。链式代理的最终出口由目标节点决定。"
          : "此策略需要 Xray 或 sing-box 内核"
      }
      isDisabled={!supported}
      label="出站 IP 策略"
      selectedKeys={[value]}
      variant="bordered"
      onSelectionChange={(keys) =>
        onChange(keys.has("ipv4_only") ? "ipv4_only" : "auto")
      }
    >
      <SelectItem key="auto">自动（IPv4 / IPv6）</SelectItem>
      <SelectItem key="ipv4_only">仅 IPv4 出站</SelectItem>
    </Select>
  );
}
