import { memo } from "react";
import {
  Activity,
  ArrowUpCircle,
  Download,
  Gauge,
  GripVertical,
  ListChecks,
  Pencil,
  RotateCcw,
  Trash2,
  BellOff,
} from "lucide-react";

import { Progress } from "@/shadcn-bridge/heroui/progress";
import { LatencyCell, probePoints } from "@/components/latency-cell";
import { Node, RemoteUsageNode } from "@/pages/node/types";
import { getNodeExpiryMeta, SortableItem } from "@/pages/node/helpers";
import { NodeStatusCard, type NodeCardAction } from "@/pages/node/status-card";

export interface NodeRowActions {
  toggleSelect: (id: number) => void;
  handleDismissExpiryReminder: (nodeId: number) => Promise<void>;
  onOpenMonitor: ((nodeId: number) => void) | undefined;
  openInstallSelector: (node: Node) => Promise<void>;
  openUpgradeModal: (
    target: "single" | "batch",
    nodeId?: number,
  ) => Promise<void>;
  handleRollbackNode: (node: Node) => void;
  handleEdit: (node: Node) => void;
  handleDelete: (node: Node) => void;
  probeNode: (id: number) => Promise<void>;
  setTargetNodes: (nodes: Node[]) => void;
  setTuneNode: (node: Node) => void;
  setSheetNodeId: (id: number) => void;
  setRouteNode: (node: Node) => void;
  setTrendNode: (node: Node) => void;
}
interface NodeRowProps {
  node: Node;
  card: boolean;
  usage?: RemoteUsageNode;
  progress?: { stage: string; percent: number; message: string };
  month?: [number, number];
  selected: boolean;
  probing: boolean;
  selectMode: boolean;
  isDisplayFiltered: boolean;
  wide: boolean;
  actions: NodeRowActions;
}
export const NodeRow = memo(function NodeRow({
  node,
  usage,
  progress,
  month,
  selected,
  probing,
  selectMode,
  actions: handlers,
}: NodeRowProps) {
  const {
    toggleSelect,
    handleDismissExpiryReminder,
    onOpenMonitor,
    openInstallSelector,
    openUpgradeModal,
    handleRollbackNode,
    handleEdit,
    handleDelete,
    probeNode,
    setTargetNodes,
    setTuneNode,
    setSheetNodeId,
    setRouteNode,
    setTrendNode,
  } = handlers;
  const renderLatency = (node: Node, variant: "card" | "list") =>
    node.isRemote !== 1 && node.connectionStatus === "online" ? (
      node.latencyTargets?.points.length === 0 ? (
        <button
          className="rounded-full bg-default-100 px-2 py-0.5 text-xs text-default-500 hover:text-default-700"
          title="这台服务器没选测试点，点此设置"
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setTargetNodes([node]);
          }}
        >
          不测延迟
        </button>
      ) : probePoints(node.latency).length === 0 ? null : (
        // Nothing until the first round (every 3 minutes) has a result;
        // re-probing is in the result's menu.
        <LatencyCell
          note={node.latencyTargets ? "自定义" : undefined}
          probe={node.latency}
          probing={probing}
          variant={variant}
          onEditTargets={() => setTargetNodes([node])}
          onProbe={() => probeNode(node.id)}
          onShowRoute={() => setRouteNode(node)}
          onShowTrend={() => setTrendNode(node)}
        />
      )
    ) : null;

  const remote = node.isRemote === 1;
  const online = node.connectionStatus === "online";
  const expiry = getNodeExpiryMeta(node.expiryTime, node.renewalCycle);
  const openDetail = () =>
    remote || !onOpenMonitor ? setSheetNodeId(node.id) : onOpenMonitor(node.id);
  const actions: NodeCardAction[] = [
    {
      key: "detail",
      label: "查看详情",
      icon: <Activity className="h-4 w-4" />,
      onPress: openDetail,
    },
    ...(!remote
      ? [
          {
            key: "edit",
            label: "编辑节点",
            icon: <Pencil className="h-4 w-4" />,
            onPress: () => handleEdit(node),
          },
          {
            key: "install",
            label: node.copyLoading ? "正在获取安装命令…" : "安装命令",
            icon: <Download className="h-4 w-4" />,
            disabled: node.copyLoading,
            onPress: () => openInstallSelector(node),
          },
          {
            key: "targets",
            label: "测试点设置",
            icon: <ListChecks className="h-4 w-4" />,
            onPress: () => setTargetNodes([node]),
          },
          {
            key: "upgrade",
            label: node.upgradeLoading ? "正在升级…" : "升级 Agent",
            icon: <ArrowUpCircle className="h-4 w-4" />,
            disabled: !online || node.upgradeLoading,
            onPress: () => openUpgradeModal("single", node.id),
          },
          {
            key: "rollback",
            label: node.rollbackLoading ? "正在回退…" : "回退版本",
            icon: <RotateCcw className="h-4 w-4" />,
            disabled: !online || node.rollbackLoading,
            onPress: () => handleRollbackNode(node),
          },
          {
            key: "tune",
            label: "系统调优",
            icon: <Gauge className="h-4 w-4" />,
            disabled: !online,
            onPress: () => setTuneNode(node),
          },
          ...(expiry.isHighlighted && !node.expiryReminderDismissed
            ? [
                {
                  key: "dismiss",
                  label: "关闭到期提醒",
                  icon: <BellOff className="h-4 w-4" />,
                  onPress: () => handleDismissExpiryReminder(node.id),
                },
              ]
            : []),
        ]
      : []),
    {
      key: "delete",
      label: "删除节点",
      icon: <Trash2 className="h-4 w-4" />,
      danger: true,
      onPress: () => handleDelete(node),
    },
  ];

  return (
    <SortableItem key={node.id} id={node.id}>
      {(listeners, attributes) => (
        <NodeStatusCard
          actions={actions}
          dragHandle={
            <button
              {...attributes}
              {...listeners}
              aria-label={`拖拽排序 ${node.name}`}
              className="flex h-7 w-5 cursor-grab items-center justify-center text-default-400 opacity-40 transition-opacity hover:text-foreground active:cursor-grabbing group-hover:opacity-100 focus-visible:opacity-100"
              style={{ touchAction: "none" }}
              type="button"
            >
              <GripVertical className="h-3.5 w-3.5" />
            </button>
          }
          latency={renderLatency(node, "card")}
          month={month}
          node={node}
          progress={
            progress && progress.percent < 100 ? (
              <Progress
                showValueLabel
                aria-label="升级进度"
                color="warning"
                label={progress.message}
                size="sm"
                value={progress.percent}
              />
            ) : undefined
          }
          selectMode={selectMode}
          selected={selected}
          usage={usage}
          onOpen={openDetail}
          onSelect={() => toggleSelect(node.id)}
        />
      )}
    </SortableItem>
  );
});
