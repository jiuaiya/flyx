import type { InboundApiItem, InboundRelayApiItem } from "@/api/types";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import toast from "react-hot-toast";

import { getStoredValue, setStoredValue } from "@/utils/identity-storage";
import {
  markPageCached,
  pageCached,
  useCachedState,
} from "@/hooks/use-cached-state";
import { useVisiblePolling } from "@/hooks/use-visible-polling";
import { latestProbe } from "@/utils/latest-probe";
import { getInboundList, getInboundRelays, probeInboundRelays } from "@/api";
import { AnimatedPage } from "@/components/animated-page";
import { PageLoadingState } from "@/components/page-state";
import {
  EmptyHint,
  PageHeader,
  PageTabs,
  PageToolbar,
  ToolbarButton,
} from "@/components/page-kit";
import { RelayLatencyCell } from "@/components/relay-latency";
import ForwardPage from "@/pages/forward";
import { RelayModal } from "@/pages/inbound/relay-modal";
import TunnelPage from "@/pages/tunnel";
import { TunnelMonitorView } from "@/components/lazy-charts";
import { RelayLandingCard } from "@/pages/relay/relay-landing-card";

type RelayTab = "lines" | "tunnels" | "rules" | "quality";

const TAB_KEY = "flyx_relay_tab";

function readTab(): RelayTab {
  try {
    const v = getStoredValue(localStorage, TAB_KEY);

    if (v === "lines" || v === "tunnels" || v === "rules" || v === "quality")
      return v;
  } catch {
    // Remembering the tab is only a convenience.
  }

  return "lines";
}

const TABS: Array<{ key: RelayTab; label: string }> = [
  { key: "lines", label: "线路" },
  { key: "tunnels", label: "隧道" },
  { key: "rules", label: "规则" },
  { key: "quality", label: "质量" },
];

/** Tunnel latency, throughput and errors over time (was 监控 → 隧道). */
function QualityView() {
  return <TunnelMonitorView />;
}

const LINES_PAGE = "relay-lines";

/** Relay lines grouped by the landing they lead to. */
function LinesView() {
  // A revisit shows the last data at once and reloads in the background.
  const [loading, setLoading] = useState(() => !pageCached(LINES_PAGE));
  const [inbounds, setInbounds] = useCachedState<InboundApiItem[]>(
    LINES_PAGE,
    "inbounds",
    [],
  );
  const [relays, setRelays] = useCachedState<InboundRelayApiItem[]>(
    LINES_PAGE,
    "relays",
    [],
  );
  const [editing, setEditing] = useState<InboundApiItem | null>(null);
  const [onlyWithRelays, setOnlyWithRelays] = useState(false);
  const [query, setQuery] = useState("");
  const [probingIds, setProbingIds] = useState<Set<number>>(new Set());
  const loadingRef = useRef(false);
  // A reload asked for during a silent poll (after an edit) runs after it.
  const reloadRef = useRef(false);

  const load = useCallback(async (silent = false): Promise<void> => {
    if (loadingRef.current) {
      if (!silent) reloadRef.current = true;

      return;
    }
    loadingRef.current = true;
    try {
      const [inRes, relRes] = await Promise.all([
        getInboundList(),
        getInboundRelays(0),
      ]);

      if (inRes.code === 0) setInbounds(inRes.data ?? []);
      if (relRes.code === 0) {
        setRelays((prev) => {
          const previous = new Map(prev.map((r) => [r.id, r]));

          return (relRes.data ?? []).map((r) => ({
            ...r,
            latency: latestProbe(r.latency, previous.get(r.id)?.latency),
          }));
        });
      }
      if (inRes.code === 0 && relRes.code === 0) markPageCached(LINES_PAGE);
      if (!silent && (inRes.code !== 0 || relRes.code !== 0))
        toast.error(inRes.msg || relRes.msg || "加载失败");
    } catch {
      if (!silent) toast.error("加载失败");
    } finally {
      loadingRef.current = false;
      setLoading(false);
      if (reloadRef.current) {
        reloadRef.current = false;
        void load(true);
      }
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);
  useVisiblePolling(useCallback(() => load(true), [load]));

  /** Measures the given lines (all when empty) and merges the results. */
  const probe = useCallback(
    async (ids: number[]) => {
      const busy = ids.length > 0 ? ids : relays.map((r) => r.id);

      setProbingIds((prev) => new Set([...prev, ...busy]));
      try {
        const res = await probeInboundRelays(ids);

        if (res.code !== 0) {
          toast.error(res.msg || "检测失败");

          return;
        }
        const data = res.data ?? {};

        setRelays((prev) =>
          prev.map((r) =>
            data[String(r.id)]
              ? { ...r, latency: latestProbe(data[String(r.id)], r.latency) }
              : r,
          ),
        );
      } catch {
        toast.error("检测失败");
      } finally {
        setProbingIds((prev) => {
          const next = new Set(prev);

          busy.forEach((id) => next.delete(id));

          return next;
        });
      }
    },
    [relays],
  );

  const latencyOf = (l: InboundRelayApiItem) =>
    l.forwardExists ? (
      <RelayLatencyCell
        latency={l.latency}
        probing={probingIds.has(l.id)}
        variant="summary"
        onProbe={() => probe([l.id])}
      />
    ) : null;

  const byInbound = useMemo(() => {
    const m = new Map<number, InboundRelayApiItem[]>();

    for (const r of relays) {
      m.set(r.inboundId, [...(m.get(r.inboundId) ?? []), r]);
    }

    return m;
  }, [relays]);

  if (loading) return <PageLoadingState message="正在加载..." />;

  const q = query.trim().toLowerCase();
  const shown = inbounds
    .filter(
      (i) =>
        (!onlyWithRelays || (byInbound.get(i.id)?.length ?? 0) > 0) &&
        (!q || i.name.toLowerCase().includes(q)),
    )
    .sort(
      (a, b) =>
        Number((byInbound.get(b.id)?.length ?? 0) > 0) -
        Number((byInbound.get(a.id)?.length ?? 0) > 0),
    );

  return (
    <div className="space-y-3">
      <PageToolbar
        placeholder="搜索节点名称"
        search={query}
        onSearch={setQuery}
      >
        <ToolbarButton
          active={onlyWithRelays}
          onPress={() => setOnlyWithRelays((v) => !v)}
        >
          只看有中转的
        </ToolbarButton>
        {relays.length > 0 && (
          <ToolbarButton onPress={() => probe([])}>
            {probingIds.size > 0 ? "测延迟中…" : "测延迟"}
          </ToolbarButton>
        )}
      </PageToolbar>

      {shown.length === 0 ? (
        <EmptyHint
          text={
            inbounds.length === 0
              ? "还没有节点。先在“节点”里搭建或导入，再回来对接中转"
              : query
                ? "没有匹配的节点"
                : "还没有中转线路"
          }
        />
      ) : (
        <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-2">
          {shown.map((i) => {
            const lines = byInbound.get(i.id) ?? [];

            return (
              <RelayLandingCard
                key={i.id}
                inbound={i}
                lines={lines}
                renderLatency={latencyOf}
                onManage={() => setEditing(i)}
              />
            );
          })}
        </div>
      )}

      <RelayModal
        item={editing}
        onChanged={() => load()}
        onClose={() => setEditing(null)}
      />
    </div>
  );
}

/** 中转: relay lines per landing, plus the tunnel and rule pages. */
export default function RelayPage() {
  const [tab, setTabState] = useState<RelayTab>(readTab);
  const setTab = (t: RelayTab) => {
    setTabState(t);
    try {
      setStoredValue(localStorage, TAB_KEY, t);
    } catch {
      // ignore
    }
  };

  return (
    <AnimatedPage className="px-3 py-6 lg:px-6">
      <PageHeader
        description="让客户端先连中转再到节点：线路按节点管理，隧道和规则是底层的转发配置"
        title="中转"
      />
      <PageTabs tabs={TABS} value={tab} onChange={setTab} />
      {tab === "lines" ? (
        <LinesView />
      ) : tab === "tunnels" ? (
        <TunnelPage embedded />
      ) : tab === "quality" ? (
        <QualityView />
      ) : (
        <ForwardPage embedded />
      )}
    </AnimatedPage>
  );
}
