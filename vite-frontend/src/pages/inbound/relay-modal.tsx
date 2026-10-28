import type {
  InboundApiItem,
  InboundRelayApiItem,
  NodeApiItem,
  TunnelApiItem,
} from "@/api/types";

import { useCallback, useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";

import {
  createInboundRelay,
  deleteInboundRelay,
  getInboundRelays,
  probeInboundRelays,
  getTunnelList,
  getNodeList,
  updateInboundRelay,
} from "@/api";
import { RelayLatencyCell } from "@/components/relay-latency";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import {
  relayEntryIPs,
  relayIPLabel,
  relayTargetIPs,
  type RelayIPFamily,
} from "@/pages/inbound/relay-addresses";

const modalBase =
  "!w-[calc(100%-32px)] !mx-auto sm:!w-full rounded-2xl overflow-hidden";

function isLoopback(listen: string) {
  return listen === "::1" || listen.startsWith("127.");
}

/** Why a tunnel cannot front this inbound, or null when it can. */
export function relayBlocker(
  tunnel: TunnelApiItem,
  inbound: InboundApiItem,
): string | null {
  if (tunnel.status !== 1) return "隧道已禁用";
  const exits = tunnelExits(tunnel);

  if (tunnel.type === 2 && exits.includes(inbound.nodeId)) {
    return exits.length > 1 ? "出口为节点所在服务器时只能有这一个出口" : null;
  }

  return isLoopback(inbound.listen)
    ? "入站只监听本机，需用出口为节点所在服务器的隧道转发"
    : null;
}

function tunnelExits(tunnel: TunnelApiItem): number[] {
  return (tunnel.outNodeId ?? []).map((n) => n.nodeId);
}

/** How the relay reaches the inbound, shown next to the tunnel name. */
export function relayRoute(
  tunnel: TunnelApiItem,
  inbound: InboundApiItem,
): string {
  if (tunnel.type !== 2) return "端口转发";

  return tunnelExits(tunnel).includes(inbound.nodeId)
    ? "隧道转发到本机"
    : "隧道经出口服务器转发到公网地址";
}

export function RelayModal({
  item,
  onClose,
  onChanged,
}: {
  item: InboundApiItem | null;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [relays, setRelays] = useState<InboundRelayApiItem[]>([]);
  const [tunnels, setTunnels] = useState<TunnelApiItem[]>([]);
  const [nodes, setNodes] = useState<NodeApiItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [form, setForm] = useState({
    tunnelId: "",
    inPort: "",
    name: "",
    shareHost: "",
    inIp: "",
    targetIpFamily: "auto" as RelayIPFamily,
  });
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<{
    id: number;
    name: string;
    shareHost: string;
  } | null>(null);
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState<InboundRelayApiItem | null>(null);
  const [removeFailed, setRemoveFailed] = useState(false);
  const [removeLoading, setRemoveLoading] = useState(false);
  const [probingId, setProbingId] = useState(0);

  const probeRelay = async (id: number) => {
    setProbingId(id);
    try {
      const res = await probeInboundRelays([id]);
      const latency = res.data?.[String(id)];

      if (res.code !== 0) toast.error(res.msg || "检测失败");
      else if (latency) {
        setRelays((prev) =>
          prev.map((r) => (r.id === id ? { ...r, latency } : r)),
        );
      }
    } catch {
      toast.error("检测失败");
    } finally {
      setProbingId(0);
    }
  };

  const load = useCallback(async () => {
    if (!item) return;
    setLoading(true);
    try {
      const [relRes, tunRes, nodeRes] = await Promise.all([
        getInboundRelays(item.id),
        getTunnelList(),
        getNodeList(),
      ]);

      if (relRes.code === 0) setRelays(relRes.data || []);
      if (tunRes.code === 0) setTunnels(tunRes.data || []);
      if (nodeRes.code === 0) setNodes(nodeRes.data || []);
    } finally {
      setLoading(false);
    }
  }, [item]);

  useEffect(() => {
    if (!item) return;
    setForm({
      tunnelId: "",
      inPort: "",
      name: "",
      shareHost: "",
      inIp: "",
      targetIpFamily: "auto",
    });
    setEditing(null);
    load();
  }, [item, load]);

  const options = useMemo(
    () =>
      item
        ? tunnels.map((t) => ({
            tunnel: t,
            blocker: relayBlocker(t, item),
            route: relayRoute(t, item),
          }))
        : [],
    [tunnels, item],
  );
  const usable = options.filter((o) => !o.blocker);
  const selectedRoute = options.find(
    (o) => String(o.tunnel.id) === form.tunnelId,
  )?.route;
  const selectedTunnel = tunnels.find((t) => String(t.id) === form.tunnelId);
  const entryIPs = relayEntryIPs(selectedTunnel, nodes);
  const targetIPs = relayTargetIPs(item, selectedTunnel, nodes);
  const ipv6Entry = entryIPs.find((ip) => ip.includes(":"));
  const multiEntry = (selectedTunnel?.inNodeId?.length ?? 0) > 1;
  const nftEntry = selectedTunnel?.forwardMode === "nftables";

  const create = async () => {
    if (!item || !form.tunnelId) {
      toast.error("请选择隧道");

      return;
    }
    if (form.inPort.trim()) {
      const p = Number(form.inPort);

      if (!Number.isInteger(p) || p < 1 || p > 65535) {
        toast.error("入口端口必须在 1-65535 之间");

        return;
      }
    }
    setCreating(true);
    try {
      const res = await createInboundRelay({
        inboundId: item.id,
        tunnelId: Number(form.tunnelId),
        inPort: form.inPort.trim() ? Number(form.inPort) : undefined,
        name: form.name.trim() || undefined,
        shareHost: form.shareHost.trim() || undefined,
        inIp: form.inIp || undefined,
        targetIpFamily: form.targetIpFamily,
      });

      if (res.code !== 0) {
        toast.error(res.msg || "对接失败", { duration: 6000 });

        return;
      }
      toast.success("已创建中转转发");
      setForm({
        tunnelId: "",
        inPort: "",
        name: "",
        shareHost: "",
        inIp: "",
        targetIpFamily: "auto",
      });
      await load();
      onChanged();
    } finally {
      setCreating(false);
    }
  };

  const saveEdit = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("名称不能为空");

      return;
    }
    setSaving(true);
    try {
      const res = await updateInboundRelay({
        id: editing.id,
        name: editing.name.trim(),
        shareHost: editing.shareHost.trim(),
      });

      if (res.code !== 0) {
        toast.error(res.msg || "保存失败");

        return;
      }
      toast.success("已保存");
      setEditing(null);
      await load();
      onChanged();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (force: boolean) => {
    if (!removing) return;
    setRemoveLoading(true);
    try {
      const res = await deleteInboundRelay(removing.id, force);

      if (res.code !== 0) {
        setRemoveFailed(true);
        toast.error(res.msg || "删除失败");

        return;
      }
      toast.success("已删除");
      setRemoving(null);
      await load();
      onChanged();
    } finally {
      setRemoveLoading(false);
    }
  };

  return (
    <>
      <Modal
        backdrop="blur"
        classNames={{ base: modalBase }}
        isOpen={Boolean(item)}
        placement="center"
        scrollBehavior="inside"
        size="2xl"
        onOpenChange={(open) => !open && onClose()}
      >
        <ModalContent>
          {() => (
            <>
              <ModalHeader closable className="flex flex-col gap-1">
                <h2 className="text-lg font-bold">中转 · {item?.name}</h2>
                <p className="text-small text-default-500">
                  在中转机上创建一条指向该入站的转发，订阅会自动加入中转节点
                </p>
              </ModalHeader>
              <ModalBody>
                <div className="space-y-4">
                  <div className="space-y-2">
                    {loading && (
                      <p className="text-sm text-default-500">加载中...</p>
                    )}
                    {!loading && relays.length === 0 && (
                      <p className="text-sm text-default-500">
                        还没有对接中转。
                      </p>
                    )}
                    {relays.map((rel) =>
                      editing?.id === rel.id ? (
                        <div
                          key={rel.id}
                          className="space-y-3 rounded-xl border border-primary/40 px-3 py-3"
                        >
                          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                            <Input
                              label="名称"
                              value={editing.name}
                              variant="bordered"
                              onChange={(e) =>
                                setEditing((p) =>
                                  p ? { ...p, name: e.target.value } : p,
                                )
                              }
                            />
                            <Input
                              label="分享地址"
                              placeholder="默认入口 IP"
                              value={editing.shareHost}
                              variant="bordered"
                              onChange={(e) =>
                                setEditing((p) =>
                                  p ? { ...p, shareHost: e.target.value } : p,
                                )
                              }
                            />
                          </div>
                          <div className="flex justify-end gap-2">
                            <Button
                              size="sm"
                              variant="light"
                              onPress={() => setEditing(null)}
                            >
                              取消
                            </Button>
                            <Button
                              color="primary"
                              isLoading={saving}
                              size="sm"
                              variant="flat"
                              onPress={saveEdit}
                            >
                              保存
                            </Button>
                          </div>
                        </div>
                      ) : (
                        <div
                          key={rel.id}
                          className="flex items-center justify-between gap-3 rounded-xl border border-divider px-3 py-2"
                        >
                          <div className="min-w-0 flex-1">
                            <div className="flex min-w-0 items-center gap-2">
                              <span className="min-w-0 font-medium truncate">
                                {rel.name}
                              </span>
                              <Chip
                                className="shrink-0 whitespace-nowrap"
                                size="sm"
                                variant="flat"
                              >
                                {rel.mode === "tunnel"
                                  ? "隧道转发"
                                  : "端口转发"}
                              </Chip>
                              {!rel.forwardExists ? (
                                <Chip
                                  className="shrink-0 whitespace-nowrap"
                                  color="danger"
                                  size="sm"
                                  variant="flat"
                                >
                                  转发已删除
                                </Chip>
                              ) : rel.forwardStatus !== 1 ? (
                                <Chip
                                  className="shrink-0 whitespace-nowrap"
                                  color="warning"
                                  size="sm"
                                  variant="flat"
                                >
                                  转发已暂停
                                </Chip>
                              ) : null}
                            </div>
                            <p className="text-xs text-default-500 font-mono mt-0.5 break-all">
                              {rel.tunnelName} ·{" "}
                              {rel.entries.length > 0
                                ? rel.entries
                                    .map(
                                      (e) =>
                                        `${e.Host.includes(":") ? `[${e.Host}]` : e.Host}:${e.Port}`,
                                    )
                                    .join(", ")
                                : "无入口"}{" "}
                              → {rel.remoteAddr}
                            </p>
                          </div>
                          {rel.forwardExists && (
                            <RelayLatencyCell
                              latency={rel.latency}
                              probing={probingId === rel.id}
                              onProbe={() => probeRelay(rel.id)}
                            />
                          )}
                          <Button
                            size="sm"
                            variant="flat"
                            onPress={() =>
                              setEditing({
                                id: rel.id,
                                name: rel.name,
                                shareHost: rel.shareHost,
                              })
                            }
                          >
                            编辑
                          </Button>
                          <Button
                            color="danger"
                            size="sm"
                            variant="flat"
                            onPress={() => {
                              setRemoveFailed(false);
                              setRemoving(rel);
                            }}
                          >
                            删除
                          </Button>
                        </div>
                      ),
                    )}
                  </div>

                  <div className="space-y-3 rounded-xl border border-divider p-3">
                    <p className="text-sm font-medium">添加中转</p>
                    {usable.length === 0 && !loading && (
                      <p className="text-xs text-warning">
                        没有可用的隧道：请先在“隧道”页创建以中转机为入口的端口转发或隧道转发。
                      </p>
                    )}
                    <Select
                      disabledKeys={options
                        .filter((o) => o.blocker)
                        .map((o) => String(o.tunnel.id))}
                      label="隧道"
                      placeholder="选择中转隧道"
                      selectedKeys={form.tunnelId ? [form.tunnelId] : []}
                      onSelectionChange={(keys) =>
                        setForm((p) => ({
                          ...p,
                          tunnelId: String(Array.from(keys)[0] ?? ""),
                          inIp: "",
                          targetIpFamily: "auto",
                        }))
                      }
                    >
                      {options.map(({ tunnel, blocker, route }) => {
                        const text = `${tunnel.name}（${route}）${
                          blocker ? ` - ${blocker}` : ""
                        }`;

                        return (
                          <SelectItem key={String(tunnel.id)} textValue={text}>
                            {text}
                          </SelectItem>
                        );
                      })}
                    </Select>
                    {selectedRoute === "隧道经出口服务器转发到公网地址" && (
                      <p className="text-xs text-default-500">
                        流量经隧道到出口服务器（如
                        IX），再由出口服务器连接节点的公网地址。
                        开启入站白名单后，会自动放行出口服务器的地址。
                      </p>
                    )}
                    {selectedTunnel && (
                      <div className="space-y-3 rounded-lg bg-default-50 p-3">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <p className="text-sm font-medium">连接地址</p>
                          {ipv6Entry && targetIPs.ipv4 && (
                            <Button
                              color="primary"
                              size="sm"
                              variant="flat"
                              onPress={() =>
                                setForm((p) => ({
                                  ...p,
                                  inIp: ipv6Entry,
                                  targetIpFamily: "ipv4",
                                }))
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
                            isDisabled={entryIPs.length === 0}
                            label="入口监听 IP"
                            selectedKeys={[form.inIp || "__default__"]}
                            onSelectionChange={(keys) => {
                              const ip = String(
                                Array.from(keys)[0] ?? "__default__",
                              );

                              setForm((p) => ({
                                ...p,
                                inIp: ip === "__default__" ? "" : ip,
                              }));
                            }}
                          >
                            <SelectItem key="__default__">
                              服务器默认监听
                            </SelectItem>
                            {entryIPs.map((ip) => (
                              <SelectItem key={ip}>
                                {relayIPLabel(ip)}
                              </SelectItem>
                            ))}
                          </Select>
                          <Select
                            description="中转或出口服务器连接落地时使用的地址"
                            disabledKeys={["ipv4", "ipv6"].filter(
                              (family) => !targetIPs[family as RelayIPFamily],
                            )}
                            label="连接落地地址"
                            selectedKeys={[form.targetIpFamily]}
                            onSelectionChange={(keys) =>
                              setForm((p) => ({
                                ...p,
                                targetIpFamily: String(
                                  Array.from(keys)[0] ?? "auto",
                                ) as RelayIPFamily,
                              }))
                            }
                          >
                            <SelectItem key="auto">自动选择</SelectItem>
                            <SelectItem key="ipv4">
                              {targetIPs.ipv4
                                ? `IPv4 · ${targetIPs.ipv4}`
                                : "IPv4（无可用地址）"}
                            </SelectItem>
                            <SelectItem key="ipv6">
                              {targetIPs.ipv6
                                ? `IPv6 · ${targetIPs.ipv6}`
                                : "IPv6（无可用地址）"}
                            </SelectItem>
                          </Select>
                        </div>
                        <p className="break-all text-xs text-default-500">
                          {form.inIp ? relayIPLabel(form.inIp) : "默认入口"}
                          {" → "}
                          {form.targetIpFamily === "auto"
                            ? "自动连接落地"
                            : `${form.targetIpFamily === "ipv6" ? "IPv6" : "IPv4"} · ${targetIPs[form.targetIpFamily] || "地址不可用"}`}
                        </p>
                      </div>
                    )}
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                      <Input
                        label="入口端口"
                        placeholder="留空自动"
                        type="number"
                        value={form.inPort}
                        variant="bordered"
                        onChange={(e) =>
                          setForm((p) => ({ ...p, inPort: e.target.value }))
                        }
                      />
                      <Input
                        label="名称"
                        placeholder="默认：隧道 → 入站"
                        value={form.name}
                        variant="bordered"
                        onChange={(e) =>
                          setForm((p) => ({ ...p, name: e.target.value }))
                        }
                      />
                      <Input
                        description="留空自动使用入口地址，可填写自定义域名"
                        label="分享地址"
                        placeholder={form.inIp || "自动跟随入口 IP"}
                        value={form.shareHost}
                        variant="bordered"
                        onChange={(e) =>
                          setForm((p) => ({ ...p, shareHost: e.target.value }))
                        }
                      />
                    </div>
                    <Button
                      color="primary"
                      isDisabled={!form.tunnelId}
                      isLoading={creating}
                      size="sm"
                      variant="flat"
                      onPress={create}
                    >
                      创建中转
                    </Button>
                  </div>
                </div>
              </ModalBody>
            </>
          )}
        </ModalContent>
      </Modal>

      <Modal
        backdrop="blur"
        classNames={{ base: modalBase }}
        isOpen={Boolean(removing)}
        placement="center"
        onOpenChange={(open) => !open && setRemoving(null)}
      >
        <ModalContent>
          {(close) => (
            <>
              <ModalHeader>
                <h2 className="text-lg font-bold text-danger">删除中转</h2>
              </ModalHeader>
              <ModalBody>
                <p className="text-sm text-default-600">
                  会同时删除中转机上的转发 “
                  {removing?.forwardName || removing?.name}
                  ”，订阅中对应的节点随之消失。
                </p>
                {removeFailed && (
                  <p className="text-small text-warning">
                    中转服务器上的转发删除失败（可能离线），可以强制删除面板记录。
                  </p>
                )}
              </ModalBody>
              <ModalFooter>
                <Button variant="light" onPress={close}>
                  取消
                </Button>
                {removeFailed && (
                  <Button
                    color="warning"
                    isLoading={removeLoading}
                    onPress={() => remove(true)}
                  >
                    强制删除
                  </Button>
                )}
                <Button
                  color="danger"
                  isLoading={removeLoading}
                  onPress={() => remove(false)}
                >
                  删除
                </Button>
              </ModalFooter>
            </>
          )}
        </ModalContent>
      </Modal>
    </>
  );
}
