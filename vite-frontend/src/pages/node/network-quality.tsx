import type { NodeQualityData, NodeQualityStats } from "@/api/types";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  ArrowDownUp,
  CheckCircle2,
  Clock3,
  Download,
  Link2,
  Maximize,
  RefreshCw,
  RotateCcw,
  Signal,
  TriangleAlert,
} from "lucide-react";
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import toast from "react-hot-toast";

import { getNodeQuality } from "@/api";
import {
  analyticsButton,
  analyticsDate,
  AnalyticsState,
  saveChartImage,
} from "@/pages/node/analytics-chart";

const colors = [
  "#a78bfa",
  "#f87171",
  "#34d399",
  "#60a5fa",
  "#fbbf24",
  "#22d3ee",
  "#fb923c",
  "#e879f9",
];
const ranges = [
  [1, "1h"],
  [6, "6h"],
  [24, "24h"],
  [168, "7d"],
  [720, "30d"],
] as const;
const numberLabel = (n: number | null | undefined) =>
  n == null
    ? "—"
    : new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(n);
const statLabel = (
  s: NodeQualityStats | undefined,
  key: keyof NodeQualityStats,
) => numberLabel(s?.[key]);

function QualityTooltip({
  active,
  payload,
  label,
}: {
  active?: boolean;
  payload?: {
    dataKey: string;
    name: string;
    value: number | null;
    color: string;
    payload: Record<string, number | null>;
  }[];
  label?: number;
}) {
  if (!active || !payload?.length || label == null) return null;

  return (
    <div className="min-w-48 rounded-xl border border-divider bg-content1 p-3 text-xs shadow-xl">
      <p className="mb-2 text-default-500">{analyticsDate(label)}</p>
      {payload.map((p) => {
        const loss = p.payload[`${p.dataKey}loss`];

        return (
          <div
            key={p.dataKey}
            className="mt-1.5 flex items-center justify-between gap-5"
          >
            <span className="flex items-center gap-1.5">
              <span
                className="h-2 w-2 rounded-full"
                style={{ background: p.color }}
              />
              {p.name}
            </span>
            <span className="font-mono">
              {p.value == null
                ? loss == null
                  ? "未采集"
                  : "无响应"
                : `${numberLabel(p.value)} ms`}
              <span className="ml-2 text-default-500">
                {loss == null
                  ? "缺少采样"
                  : loss > 0
                    ? `丢包 ${loss}%`
                    : "良好"}
              </span>
            </span>
          </div>
        );
      })}
    </div>
  );
}

export function NodeNetworkQuality({
  nodeId,
  nodeName,
  variant = "detail",
}: {
  nodeId: number;
  nodeName: string;
  /** The modal shares the trend and anomaly analysis without the page overview. */
  variant?: "detail" | "trend";
}) {
  const trendOnly = variant === "trend";
  const [hours, setHours] = useState(24);
  const [interval, setIntervalMinutes] = useState(0);
  const [selected, setSelected] = useState<string[]>([]);
  const [compare, setCompare] = useState(false);
  const [worst, setWorst] = useState(false);
  const [connect, setConnect] = useState(true);
  const [zoomMode, setZoomMode] = useState(false);
  const [zoom, setZoom] = useState<[number, number] | null>(null);
  const [selection, setSelection] = useState<[number, number] | null>(null);
  const [focus, setFocus] = useState<number | null>(null);
  const [revision, setRevision] = useState(0);
  const [data, setData] = useState<NodeQualityData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const chart = useRef<HTMLDivElement>(null);
  const drag = useRef<number | null>(null);
  const graph = useRef<HTMLDivElement>(null);
  const worstNames = useMemo(
    () =>
      [...(data?.points ?? [])]
        .sort(
          (a, b) =>
            (b.summary.lossPct ?? -1) - (a.summary.lossPct ?? -1) ||
            b.avgMs - a.avgMs,
        )
        .slice(0, 10)
        .map((p) => p.name),
    [data],
  );
  const targets = selected.length ? selected : worst ? worstNames : [];
  const targetsKey = JSON.stringify(targets);

  useEffect(() => {
    let cancelled = false;

    setLoading(true);
    setError(null);
    void getNodeQuality(
      nodeId,
      hours,
      interval,
      JSON.parse(targetsKey) as string[],
    )
      .then((res) => {
        if (cancelled) return;
        if (res.code !== 0 || !res.data || !Array.isArray(res.data.points))
          throw new Error(res.msg || "加载网络质量失败");
        setData(res.data);
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : "加载网络质量失败");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [nodeId, hours, interval, targetsKey, revision]);

  useEffect(() => {
    const timer = window.setInterval(() => setRevision((v) => v + 1), 60000);

    return () => window.clearInterval(timer);
  }, []);

  const shown = (data?.points ?? []).filter(
    (p) => !targets.length || targets.includes(p.name),
  );
  const targetList = worst
    ? (data?.points ?? [])
        .filter((p) => worstNames.includes(p.name))
        .sort((a, b) => worstNames.indexOf(a.name) - worstNames.indexOf(b.name))
    : (data?.points ?? []);
  const chartRows = useMemo(() => {
    if (!data) return [];
    const rows = new Map<number, Record<string, number | null>>();
    const expected = Math.max(data.intervalMs, data.probeIntervalMs);

    data.points.forEach((point, index) => {
      let previous: number | null = null;

      point.samples.forEach((sample) => {
        if (previous != null && sample.t - previous > expected * 1.6) {
          const t = previous + expected;

          rows.set(t, rows.get(t) ?? { t });
        }
        const row = rows.get(sample.t) ?? { t: sample.t };

        row[`s${index}`] = sample.ms;
        row[`s${index}loss`] = sample.lossPct;
        rows.set(sample.t, row);
        previous = sample.t;
      });
    });

    return [...rows.values()].sort((a, b) => Number(a.t) - Number(b.t));
  }, [data]);
  const plotted = zoom
    ? chartRows.filter(
        (row) => Number(row.t) >= zoom[0] && Number(row.t) <= zoom[1],
      )
    : chartRows;
  const summary = data?.summary;
  const rangeLabel = ranges.find(([h]) => h === hours)?.[1];
  const refresh = () => setRevision((v) => v + 1);
  const resetZoom = () => {
    setZoom(null);
    setFocus(null);
    setSelection(null);
    drag.current = null;
  };
  const chooseTarget = (name: string) => {
    setSelected((current) =>
      compare || trendOnly
        ? current.includes(name)
          ? current.filter((n) => n !== name)
          : [...current, name]
        : current.length === 1 && current[0] === name
          ? []
          : [name],
    );
    resetZoom();
  };
  const finishZoom = () => {
    if (
      selection &&
      Math.abs(selection[1] - selection[0]) >= (data?.intervalMs ?? 1)
    ) {
      setZoom([Math.min(...selection), Math.max(...selection)]);
      setFocus(null);
    }
    setSelection(null);
    drag.current = null;
  };

  return (
    <div aria-busy={loading} className="space-y-4">
      {!trendOnly && (
        <>
          <section className="flex items-center gap-3 rounded-2xl border border-divider bg-content1 px-4 py-4">
            <span className="p-1 text-default-500">
              <Activity className="h-5 w-5" />
            </span>
            <div>
              <h2 className="text-lg font-semibold">网络质量</h2>
              <p className="mt-1 text-xs text-default-500">
                单节点网络质量分析：延迟、抖动、丢包与成功率
              </p>
            </div>
          </section>

          <section
            aria-label="网络质量概览"
            className="overflow-hidden rounded-2xl border border-divider bg-content1"
          >
            <div className="flex items-center justify-between border-b border-divider px-5 py-3.5">
              <h3 className="text-sm font-semibold">
                概览{" "}
                <span className="ml-1 font-normal text-default-500">
                  {rangeLabel}
                </span>
              </h3>
              <button
                aria-label="刷新网络质量"
                className={analyticsButton()}
                disabled={loading}
                type="button"
                onClick={refresh}
              >
                <RefreshCw
                  className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`}
                />
              </button>
            </div>
            <div className="grid grid-cols-2 gap-3 p-4 lg:grid-cols-4">
              {(
                [
                  [Clock3, "平均延迟", "avgMs", "ms"],
                  [ArrowDownUp, "抖动", "jitterMs", "ms"],
                  [Signal, "平均丢包率", "lossPct", "%"],
                  [CheckCircle2, "平均成功率", "successPct", "%"],
                ] as const
              ).map(([Icon, label, key, unit]) => (
                <div
                  key={key}
                  className="rounded-lg bg-default-50 p-3.5"
                  title={
                    key === "jitterMs"
                      ? "相邻成功探测的延迟差绝对值均值；中断或长时间缺采样不计入"
                      : key === "successPct"
                        ? "探测连接成功比例，等于 100% 减去平均丢包率"
                        : undefined
                  }
                >
                  <p className="flex items-center gap-2 text-xs text-default-500">
                    <Icon className="h-3.5 w-3.5" />
                    {label}
                  </p>
                  <p className="mt-2 font-mono text-xl font-semibold tabular-nums">
                    {statLabel(summary, key)}
                    <span className="ml-1.5 text-xs font-normal text-default-500">
                      {unit}
                    </span>
                  </p>
                </div>
              ))}
            </div>
          </section>
        </>
      )}

      {error || (!data && loading) || !data?.points.length ? (
        <section className="rounded-2xl border border-divider bg-content1">
          <AnalyticsState
            empty="暂无网络质量记录。在节点的测试点设置中启用监控目标后，在线节点会定时采集延迟与丢包数据。"
            error={error}
            loading={loading}
            onRetry={refresh}
          />
        </section>
      ) : (
        <>
          <div
            className={`grid items-stretch gap-4 ${trendOnly ? "" : "xl:grid-cols-[minmax(240px,1fr)_minmax(0,3fr)]"}`}
          >
            {!trendOnly && (
              <section className="overflow-hidden rounded-2xl border border-divider bg-content1">
                <div className="border-b border-divider px-4 py-4">
                  <h3 className="text-sm font-semibold">
                    监控目标{" "}
                    <span className="ml-1 text-xs font-normal text-default-500">
                      {data.points.length} 个
                    </span>
                  </h3>
                  <div className="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-xs text-default-500">
                    <label className="flex cursor-pointer items-center gap-1.5">
                      <input
                        checked={worst}
                        className="accent-primary"
                        type="checkbox"
                        onChange={(e) => {
                          setWorst(e.target.checked);
                          setSelected([]);
                          resetZoom();
                        }}
                      />
                      只看最差 10
                    </label>
                    <label className="flex cursor-pointer items-center gap-1.5">
                      <input
                        checked={compare}
                        className="accent-primary"
                        type="checkbox"
                        onChange={(e) => {
                          setCompare(e.target.checked);
                          setSelected([]);
                          resetZoom();
                        }}
                      />
                      对比模式
                    </label>
                  </div>
                </div>
                <div className="grid max-h-[430px] gap-1 overflow-y-auto p-3 sm:grid-cols-2 xl:grid-cols-1">
                  {targetList.map((point) => (
                    <button
                      key={point.name}
                      aria-pressed={selected.includes(point.name)}
                      className={`min-w-0 rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary ${selected.includes(point.name) ? "border-primary/60 bg-primary/5" : "border-transparent hover:border-divider hover:bg-default-50"}`}
                      type="button"
                      onClick={() => chooseTarget(point.name)}
                    >
                      <div className="flex items-center gap-2 text-sm font-medium">
                        <span
                          aria-hidden="true"
                          className={`h-3.5 w-3.5 shrink-0 rounded border ${selected.includes(point.name) ? "border-primary bg-primary" : "border-default-300"}`}
                        />
                        <span className="truncate">{point.name}</span>
                        <span className="ml-auto text-[10px] font-normal text-default-500">
                          TCPing
                        </span>
                      </div>
                      <p className="mt-2 flex justify-between text-xs text-default-500">
                        平均延迟{" "}
                        <span className="font-mono text-foreground">
                          {numberLabel(point.summary.avgMs)} ms
                        </span>
                      </p>
                      <p className="mt-1 text-[11px] text-default-500">
                        丢包 {numberLabel(point.summary.lossPct)}% · 抖动{" "}
                        {numberLabel(point.summary.jitterMs)} ms
                      </p>
                      <div
                        aria-label={`${point.name} 最近采样状态`}
                        className="mt-3 flex h-3 gap-[2px] overflow-hidden"
                      >
                        {point.samples.slice(-40).map((s) => (
                          <span
                            key={s.t}
                            className={`min-w-0 flex-1 rounded-[1px] ${s.ms == null ? "bg-danger" : s.lossPct > 0 ? "bg-warning" : "bg-success"}`}
                            title={`${analyticsDate(s.t)} · ${s.ms == null ? "无响应" : `${s.ms} ms`} · 丢包 ${s.lossPct}%`}
                          />
                        ))}
                      </div>
                    </button>
                  ))}
                </div>
                {selected.length > 0 && (
                  <div className="border-t border-divider p-3">
                    <button
                      className={`${analyticsButton()} w-full`}
                      type="button"
                      onClick={() => {
                        setSelected([]);
                        resetZoom();
                      }}
                    >
                      查看全部目标
                    </button>
                  </div>
                )}
              </section>
            )}

            <section
              ref={graph}
              className="min-w-0 overflow-hidden rounded-2xl border border-divider bg-content1"
            >
              <div className="flex flex-wrap items-center gap-3 border-b border-divider px-4 py-3.5">
                <h3 className="mr-auto text-sm font-semibold">
                  趋势{" "}
                  <span className="ml-2 text-xs font-normal text-default-500">
                    当前：
                    {selected.length
                      ? selected.join("、")
                      : worst
                        ? "最差 10 个目标"
                        : "全部目标"}
                  </span>
                </h3>
                <label className="flex items-center gap-2 text-xs text-default-500">
                  数据粒度
                  <select
                    aria-label="数据粒度"
                    className="h-8 rounded-md border border-divider bg-content1 px-2 text-foreground outline-none focus:ring-2 focus:ring-primary"
                    value={interval}
                    onChange={(e) => {
                      setIntervalMinutes(Number(e.target.value));
                      resetZoom();
                    }}
                  >
                    <option value={0}>默认</option>
                    <option value={1}>1 分钟</option>
                    <option value={5}>5 分钟</option>
                  </select>
                </label>
                <label className="flex cursor-pointer items-center gap-1.5 text-xs text-default-500">
                  <input
                    checked={connect}
                    className="accent-primary"
                    type="checkbox"
                    onChange={(e) => setConnect(e.target.checked)}
                  />
                  <Link2 className="h-3.5 w-3.5" />
                  连接缺失点
                </label>
                <div
                  aria-label="网络质量时间范围"
                  className="flex items-center gap-1"
                >
                  {ranges.map(([value, label]) => (
                    <button
                      key={value}
                      aria-pressed={hours === value}
                      className={analyticsButton(hours === value)}
                      type="button"
                      onClick={() => {
                        setHours(value);
                        resetZoom();
                      }}
                    >
                      {label}
                    </button>
                  ))}
                </div>
              </div>
              {trendOnly && (
                <div
                  aria-label="选择对比目标"
                  className="flex flex-wrap gap-1.5 px-4 pt-3"
                >
                  <button
                    aria-pressed={!selected.length}
                    className={analyticsButton(!selected.length)}
                    type="button"
                    onClick={() => {
                      setSelected([]);
                      resetZoom();
                    }}
                  >
                    全部目标
                  </button>
                  {data.points.map((point, i) => (
                    <button
                      key={point.name}
                      aria-pressed={selected.includes(point.name)}
                      className={analyticsButton(selected.includes(point.name))}
                      type="button"
                      onClick={() => chooseTarget(point.name)}
                    >
                      <span
                        aria-hidden="true"
                        className="h-1.5 w-1.5 rounded-full"
                        style={{ background: colors[i % colors.length] }}
                      />
                      {point.name}
                    </button>
                  ))}
                </div>
              )}
              <div className="flex flex-wrap items-center justify-end gap-1.5 px-4 pt-3">
                {trendOnly && (
                  <button
                    aria-label="刷新网络质量"
                    className={`${analyticsButton()} mr-auto`}
                    disabled={loading}
                    type="button"
                    onClick={refresh}
                  >
                    <RefreshCw
                      className={`h-3 w-3 ${loading ? "animate-spin" : ""}`}
                    />
                    刷新
                  </button>
                )}
                <button
                  aria-pressed={zoomMode}
                  className={analyticsButton(zoomMode)}
                  title="开启后在图表上拖动选择时间区间"
                  type="button"
                  onClick={() => setZoomMode((v) => !v)}
                >
                  <Maximize className="h-3 w-3" />
                  区域缩放
                </button>
                <button
                  className={analyticsButton()}
                  disabled={!zoom}
                  type="button"
                  onClick={resetZoom}
                >
                  <RotateCcw className="h-3 w-3" />
                  缩放还原
                </button>
                <button
                  className={analyticsButton()}
                  type="button"
                  onClick={() => {
                    if (chart.current)
                      void saveChartImage(
                        chart.current,
                        `${nodeName}-网络质量-${rangeLabel}`,
                      )
                        .then(() => toast.success("图片已保存"))
                        .catch((e: unknown) =>
                          toast.error(
                            e instanceof Error ? e.message : "导出失败",
                          ),
                        );
                  }}
                >
                  <Download className="h-3 w-3" />
                  保存为图片
                </button>
              </div>
              <div
                ref={chart}
                aria-label="网络质量延迟趋势图"
                className={`${trendOnly ? "h-[280px] sm:h-[320px]" : "h-[340px] sm:h-[400px]"} bg-content1 px-1 py-4 text-foreground sm:px-3 ${zoomMode ? "cursor-crosshair select-none" : ""}`}
              >
                <ResponsiveContainer
                  height="100%"
                  initialDimension={{ width: 600, height: 340 }}
                  minHeight={0}
                  minWidth={0}
                  width="100%"
                >
                  <LineChart
                    accessibilityLayer
                    data={plotted}
                    margin={{ top: 12, right: 20, bottom: 15, left: 0 }}
                    onMouseDown={(e: { activeLabel?: number | string }) => {
                      const t = Number(e?.activeLabel);

                      if (zoomMode && Number.isFinite(t)) {
                        drag.current = t;
                        setSelection([t, t]);
                      }
                    }}
                    onMouseMove={(e: { activeLabel?: number | string }) => {
                      const t = Number(e?.activeLabel);

                      if (drag.current != null && Number.isFinite(t))
                        setSelection([drag.current, t]);
                    }}
                    onMouseUp={finishZoom}
                  >
                    <CartesianGrid
                      stroke="var(--divider)"
                      strokeDasharray="0"
                      vertical={false}
                    />
                    <XAxis
                      allowDataOverflow
                      axisLine={false}
                      dataKey="t"
                      domain={zoom ?? [data.since, data.until]}
                      minTickGap={30}
                      stroke="var(--default-500)"
                      tick={{ fontSize: 11 }}
                      tickFormatter={(t: number) =>
                        analyticsDate(t, hours >= 168)
                      }
                      tickLine={false}
                      type="number"
                    />
                    <YAxis
                      axisLine={false}
                      domain={[0, "auto"]}
                      label={{
                        value: "ms",
                        position: "insideTopLeft",
                        dy: -16,
                        fill: "var(--default-500)",
                        fontSize: 11,
                        offset: 3,
                      }}
                      stroke="var(--default-500)"
                      tick={{ fontSize: 11 }}
                      tickLine={false}
                      width={48}
                    />
                    <Tooltip content={<QualityTooltip />} filterNull={false} />
                    <Legend
                      iconType="circle"
                      verticalAlign="top"
                      wrapperStyle={{ paddingBottom: 28, fontSize: 12 }}
                    />
                    {shown.map((point) => {
                      const i = data.points.findIndex(
                        (p) => p.name === point.name,
                      );

                      return (
                        <Line
                          key={point.name}
                          activeDot={{ r: 4 }}
                          connectNulls={connect}
                          dataKey={`s${i}`}
                          dot={false}
                          isAnimationActive={false}
                          name={point.name}
                          stroke={colors[i % colors.length]}
                          strokeWidth={1.6}
                          type="linear"
                        />
                      );
                    })}
                    {selection && (
                      <ReferenceArea
                        fill="var(--primary)"
                        fillOpacity={0.12}
                        stroke="var(--primary)"
                        x1={Math.min(...selection)}
                        x2={Math.max(...selection)}
                      />
                    )}
                    {focus != null && (
                      <ReferenceLine
                        label={{
                          value: "异常时间",
                          position: "insideTopRight",
                          fill: "var(--warning)",
                          fontSize: 11,
                        }}
                        stroke="var(--warning)"
                        strokeDasharray="4 4"
                        x={focus}
                      />
                    )}
                  </LineChart>
                </ResponsiveContainer>
              </div>
              <p className="border-t border-divider px-4 py-3 text-[11px] text-default-500">
                每 {Math.round(data.probeIntervalMs / 60000)} 分钟采集一次 ·
                粒度仅合并已有采样 · 抖动为相邻成功探测的延迟差均值
                {zoom && " · 正在查看缩放区间"}
              </p>
            </section>
          </div>

          <section className="overflow-hidden rounded-2xl border border-divider bg-content1">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-divider px-5 py-4">
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <TriangleAlert className="h-4 w-4 text-default-500" />
                异常摘要
              </h3>
              <span className="text-xs text-default-500">
                {analyticsDate(data.since)} — {analyticsDate(data.until)} ·
                点击可定位到主图时间点
              </span>
            </div>
            <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4 sm:p-5">
              {(
                [
                  ["P50", "p50Ms"],
                  ["P95", "p95Ms"],
                  ["P99", "p99Ms"],
                  ["样本", "sampleCount"],
                ] as const
              ).map(([label, key]) => (
                <div key={key} className="rounded-lg bg-default-50 px-4 py-3">
                  <p className="text-xs text-default-500">{label}</p>
                  <p className="mt-2 font-mono text-lg font-semibold">
                    {statLabel(summary, key)}
                    {key !== "sampleCount" && (
                      <span className="ml-1 text-xs font-normal text-default-500">
                        ms
                      </span>
                    )}
                  </p>
                </div>
              ))}
            </div>
            <div className="divide-y divide-divider border-t border-divider">
              {data.events.map((event) => (
                <button
                  key={event.kind}
                  className="flex w-full items-center justify-between gap-4 px-5 py-4 text-left text-xs hover:bg-default-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
                  type="button"
                  onClick={() => {
                    setFocus(event.t);
                    const window = Math.max(
                      data.intervalMs * 4,
                      Math.min((hours * 3600000) / 6, 3600000),
                    );

                    setZoom([
                      Math.max(data.since, event.t - window),
                      Math.min(data.until, event.t + window),
                    ]);
                    graph.current?.scrollIntoView({
                      behavior: "smooth",
                      block: "center",
                    });
                  }}
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <span
                      className={`h-1.5 w-1.5 shrink-0 rounded-full ${event.kind === "loss" ? "bg-danger" : "bg-warning"}`}
                    />
                    <span>
                      {event.kind === "latency" ? "最高延迟" : "最高丢包"}：
                      {event.name}{" "}
                      <span className="font-mono">
                        {event.kind === "latency"
                          ? `${numberLabel(event.ms)} ms`
                          : `${event.lossPct}%`}
                      </span>
                      {event.kind === "latency" &&
                        summary?.p95Ms != null &&
                        event.ms != null &&
                        event.ms > summary.p95Ms && (
                          <span className="ml-1 text-default-500">
                            （高于 P95 {summary.p95Ms} ms）
                          </span>
                        )}
                    </span>
                  </span>
                  <span className="shrink-0 text-default-500">
                    {analyticsDate(event.t)}
                  </span>
                </button>
              ))}
              <p className="px-5 py-4 text-xs text-default-500">
                {data.events.length
                  ? "暂无更多明显异常"
                  : "所选目标暂无异常记录"}
              </p>
            </div>
          </section>
        </>
      )}
    </div>
  );
}
