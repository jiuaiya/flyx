import { useEffect, useRef, useState } from "react";
import toast from "react-hot-toast";

import { Button } from "@/shadcn-bridge/heroui/button";
import { Checkbox } from "@/shadcn-bridge/heroui/checkbox";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Progress } from "@/shadcn-bridge/heroui/progress";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";
import {
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
} from "@/shadcn-bridge/heroui/modal";
import { nodeTune, type NodeTuneAction, type NodeTuneStatus } from "@/api";

const mb = (bytes: number) => `${Math.round((bytes / 1048576) * 10) / 10} MiB`;

export function NodeTuneModal({
  node,
  onClose,
}: {
  node: { id: number; name: string } | null;
  onClose: () => void;
}) {
  const [status, setStatus] = useState<NodeTuneStatus | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<NodeTuneAction | "">("");
  const [detail, setDetail] = useState<string | null>(null);
  const [showDeep, setShowDeep] = useState(false);
  const [allowTraffic, setAllowTraffic] = useState(false);
  const generation = useRef(0);
  const lastJob = useRef<NodeTuneStatus["job"]>(undefined);
  const running = status?.job?.state === "running";
  const detection = status?.detection;
  const trafficBudget = detection
    ? Math.ceil(detection.estimatedGB * 1.25 * 10) / 10
    : 0;
  const deepAvailable = Boolean(
    detection?.applied &&
      Date.now() - detection.measuredAt < 24 * 60 * 60 * 1000 &&
      trafficBudget <= 1000,
  );

  useEffect(() => {
    const current = ++generation.current;

    setStatus(null);
    setError("");
    setBusy("");
    setDetail(null);
    setShowDeep(false);
    setAllowTraffic(false);
    lastJob.current = undefined;
    if (!node) return;
    void nodeTune(node.id, "status")
      .then((res) => {
        if (generation.current !== current) return;
        if (res.code !== 0 || !res.data) {
          setError(res.msg || "读取服务器状态失败");

          return;
        }
        setStatus(res.data);
      })
      .catch(() => {
        if (generation.current === current) setError("读取服务器状态失败");
      });

    return () => {
      generation.current++;
    };
  }, [node?.id]);

  useEffect(() => {
    if (!node || !running) return;
    const current = generation.current;
    let pending = false;
    const timer = window.setInterval(() => {
      if (pending) return;
      pending = true;
      void nodeTune(node.id, "status")
        .then((res) => {
          if (generation.current !== current) return;
          if (res.code === 0 && res.data) {
            setStatus(res.data);
            setError("");
          } else setError(res.msg || "暂时无法读取进度，正在重试");
        })
        .catch(() => {
          if (generation.current === current)
            setError("暂时无法读取进度，正在重试；任务仍在服务器运行");
        })
        .finally(() => {
          pending = false;
        });
    }, 2000);

    return () => window.clearInterval(timer);
  }, [node?.id, running]);

  useEffect(() => {
    const job = status?.job;
    const previous = lastJob.current;

    if (
      job &&
      previous?.id === job.id &&
      previous.state === "running" &&
      job.state !== "running"
    ) {
      if (job.state === "succeeded") toast.success(job.message);
      else if (job.state === "failed") toast.error(job.message);
      else toast(job.message);
    }
    lastJob.current = job;
  }, [status?.job]);

  const run = async (action: NodeTuneAction) => {
    if (!node) return;
    const current = generation.current;

    setBusy(action);
    setError("");
    try {
      const res = await nodeTune(
        node.id,
        action,
        action === "deep" ? { maxTrafficGB: trafficBudget } : {},
      );

      if (generation.current !== current) return;
      if (res.code !== 0 || !res.data) {
        toast.error(res.msg || "调优请求失败");

        return;
      }
      setStatus(res.data);
      setShowDeep(false);
      setAllowTraffic(false);
    } catch {
      if (generation.current === current)
        setError("请求未完成，任务可能已开始；重新打开可查看服务器进度");
    } finally {
      if (generation.current === current) setBusy("");
    }
  };

  return (
    <Modal
      backdrop="blur"
      classNames={{
        base: "!w-[calc(100%-32px)] !mx-auto sm:!w-full rounded-2xl overflow-hidden",
      }}
      isOpen={Boolean(node)}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      onOpenChange={(open) => !open && onClose()}
    >
      <ModalContent>
        <ModalHeader className="flex flex-col gap-1">
          <h2 className="text-xl font-bold">服务器调优</h2>
          <p className="text-sm font-normal text-default-500">
            {node?.name}
            {status &&
              ` · 内核 ${status.kernel || "未知"} · 拥塞控制 ${status.congestion || "未知"}`}
          </p>
        </ModalHeader>
        <ModalBody>
          {error && (
            <p className="text-sm text-danger" role="alert">
              {error}
            </p>
          )}
          {!status ? (
            !error && (
              <div className="flex justify-center py-10">
                <Spinner />
              </div>
            )
          ) : (
            <div className="space-y-3">
              <p className="text-xs text-default-500">
                自动实测带宽，结合内存计算参数，无需填写带宽和延迟。首次会安装所需的测速依赖。测速流量发往公共对端，千兆线路基础测速约消耗
                4 GB。
              </p>
              {!status.automatic && (
                <p className="rounded-xl bg-warning-50 p-3 text-sm text-warning-700">
                  这台服务器的 Agent 不支持自动调优，请先升级 Agent。
                </p>
              )}
              {status.job && (
                <div
                  aria-live="polite"
                  className="space-y-2 rounded-xl border border-divider p-3"
                >
                  <p
                    className={
                      status.job.state === "failed"
                        ? "text-sm text-danger"
                        : "text-sm"
                    }
                  >
                    {status.job.message}
                  </p>
                  <Progress
                    aria-label="调优进度"
                    color={status.job.state === "failed" ? "danger" : "primary"}
                    size="sm"
                    value={status.job.percent}
                  />
                  {running && (
                    <p className="text-xs text-default-500">
                      关闭窗口后任务继续运行，重新打开可查看进度。
                    </p>
                  )}
                  {status.job.logs && (
                    <details className="text-xs text-default-500">
                      <summary className="cursor-pointer">查看任务日志</summary>
                      <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all">
                        {status.job.logs}
                      </pre>
                    </details>
                  )}
                </div>
              )}
              {detection && (
                <div className="space-y-2 rounded-xl border border-divider p-3 text-xs">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-medium">
                      实测带宽 {detection.bandwidthMbps} Mbps
                    </span>
                    <Chip size="sm" variant="flat">
                      {detection.bandwidthSource}
                    </Chip>
                  </div>
                  <p className="text-default-500">
                    出口 {detection.interface} · IPv{detection.family} · 对端{" "}
                    {detection.peer}:{detection.port}
                  </p>
                  <p className="text-default-500">{detection.rttSource}</p>
                  <p className="text-default-500">
                    测速时间 {new Date(detection.measuredAt).toLocaleString()}
                  </p>
                  {status.sizes && (
                    <p>
                      内存 {status.sizes.ramMb} MiB · 缓冲区上限{" "}
                      {mb(status.sizes.bufMax)}（{status.sizes.bufReason}）
                    </p>
                  )}
                  <div className="grid grid-cols-3 gap-2">
                    {(
                      [
                        ["调优前多连接", detection.before],
                        ["调优后单连接", detection.single],
                        ["调优后多连接", detection.parallel],
                      ] as const
                    ).map(([label, sample]) => (
                      <div
                        key={label}
                        className="rounded-lg bg-default-100 p-2"
                      >
                        <p className="text-default-500">{label}</p>
                        <p className="mt-1 font-medium">
                          {sample ? `${sample.mbps.toFixed(1)} Mbps` : "待验证"}
                        </p>
                        {sample && (
                          <p className="mt-1 text-[11px] text-default-500">
                            重传 {sample.retransmits} ·{" "}
                            {sample.lossPercent.toFixed(3)}%
                          </p>
                        )}
                      </div>
                    ))}
                  </div>
                  {detection.deepSummary && <p>{detection.deepSummary}</p>}
                  {detection.warnings?.map((warning) => (
                    <p key={warning} className="text-warning-600">
                      {warning}
                    </p>
                  ))}
                </div>
              )}
              {showDeep && detection && (
                <div className="space-y-3 rounded-xl border border-warning-300 bg-warning-50 p-3 text-xs">
                  <p className="font-medium">
                    深度优化：扫描限速器拐点，按结果决定是否应用出向整形。
                  </p>
                  <p>
                    预计流量约 {detection.estimatedGB.toFixed(1)} GB，本次预算{" "}
                    {trafficBudget.toFixed(1)}{" "}
                    GB，达到预算会停止。过程可能需要数分钟，线路业务流量也会计入预算。
                  </p>
                  <Checkbox
                    isSelected={allowTraffic}
                    size="sm"
                    onValueChange={setAllowTraffic}
                  >
                    允许本次测速使用上述流量
                  </Checkbox>
                  <Button
                    color="warning"
                    isDisabled={!allowTraffic || running || busy !== ""}
                    size="sm"
                    onPress={() => void run("deep")}
                  >
                    开始深度优化
                  </Button>
                </div>
              )}
              <p className="text-xs text-default-500">
                参数应用后立即生效并保存；恢复原配置会撤销 FLYX 的调优和整形。
              </p>
              {status.groups.map((group) => (
                <details
                  key={group.id}
                  className="rounded-xl border border-divider px-3 py-2.5"
                  open={detail === group.id}
                >
                  <summary
                    className="flex cursor-pointer items-center justify-between gap-2 text-sm"
                    onClick={(event) => {
                      event.preventDefault();
                      setDetail(detail === group.id ? null : group.id);
                    }}
                  >
                    {group.name}
                    <Chip
                      color={group.applied ? "success" : "default"}
                      size="sm"
                      variant="flat"
                    >
                      {group.applied ? "已满足" : "查看参数"}
                    </Chip>
                  </summary>
                  <p className="mt-2 text-xs text-default-500">{group.desc}</p>
                  <div className="mt-2 space-y-1 font-mono text-[11px] text-default-600">
                    {group.settings.map((setting) => (
                      <div key={setting.key} className="break-all">
                        {setting.key}:{" "}
                        {setting.supported ? setting.current : "不支持"}
                        {detection &&
                          setting.supported &&
                          !setting.ok &&
                          ` → ${setting.target}`}
                      </div>
                    ))}
                  </div>
                </details>
              ))}
            </div>
          )}
        </ModalBody>
        <ModalFooter className="flex-wrap gap-2">
          {(status?.hasBackup || detection) && (
            <Button
              className="mr-auto"
              isDisabled={running || busy !== ""}
              variant="light"
              onPress={() =>
                void run(status?.automatic ? "restoreAuto" : "restore")
              }
            >
              恢复原配置
            </Button>
          )}
          {running ? (
            <Button
              color="warning"
              isDisabled={busy !== "" || status?.job?.mode === "restore"}
              onPress={() => void run("cancel")}
            >
              停止任务
            </Button>
          ) : (
            <>
              <Button
                isDisabled={!status?.automatic || !deepAvailable || busy !== ""}
                variant="bordered"
                onPress={() => {
                  setShowDeep(!showDeep);
                  setAllowTraffic(false);
                }}
              >
                深度优化
              </Button>
              <Button
                color="primary"
                isDisabled={!status?.automatic || busy !== ""}
                isLoading={busy === "auto"}
                onPress={() => void run("auto")}
              >
                自动调优
              </Button>
            </>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
