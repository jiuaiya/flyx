# 线路卡片 · 方案 3 验证

final result: passed

日期：2026-10-21（Asia/Shanghai）。本次范围是现有中转页面的线路卡片，未改后端或管理弹窗的数据逻辑。

## 视觉基准与浏览器证据

- source visual truth: `/Users/rawvix/.codex/generated_images/01a15f0f-97a4-7111-9536-9c6fcce379c1/exec-4d0cfdf7-88f3-4ff1-b187-a4e7fedf5a56.png`，即对话里第三张生成图。
- implementation URL: `http://localhost:3000/#/relay`。保留现有演示数据。
- implementation screenshot: `/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/desktop-final.png`。
- source pixels: 1586 × 992。桌面 CSS viewport 与截图均为 1712 × 1000，deviceScaleFactor 为 1。
- state: 线路页、浅色、四张已配置卡片和两张空卡片、详情关闭。临时 Vite 配置在 3001 端口提供与基准一致的名称、顺序和延迟；没有修改仓库的演示数据，验证后关闭该服务。
- density normalization: 排除现有侧栏及页头。基准卡片区域 `(63,258)-(1524,975)` 等比缩放到 1332 × 654；实现区域 `(332,214)-(1664,874)` 为 1332 × 660。两者放入同一张图比较，保留 6px 的实际高度差，不将实现拉伸至基准。
- full-view comparison: `/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/comparison-final.png`。
- focused comparison: `/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/card-comparison-final.png`。首卡基准 722 × 237 等比缩放到 658 × 216；实现卡片 658 × 219。
- mobile: `/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/mobile-final.png`，CSS 和图片均为 390 × 844，deviceScaleFactor 为 1。
- dark: `/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/desktop-dark.png`。

## 比较与修正记录

1. 首次组合比较：`comparison-before.png`、`card-comparison-before.png`（位于上述证据目录）。[P2] 标题、路径名称和测试信息相对基准过小，削弱路径层级。修正为桌面 24px 标题、16px 路径名称、14px 信息文字，同时缩减外边距以保持卡片密度。修正后重新截图并组合比较：`comparison-after.png`、`card-comparison-after.png`。
2. 第二轮检查：[P2] 数量标签沿用了桥接组件的 10px 小号，阅读困难。局部提升到 12px 并增加少量内边距。异常样例还发现失败文本前出现“估算”，已改为仅成功数值显示该前缀。最终重新捕获 `desktop-final.png`、`mobile-final.png`，并检查 `comparison-final.png`、`card-comparison-final.png`。
3. 最终组合比较没有剩余需要修改的 P0/P1/P2 项。主题、两列路径结构、标题层级、状态与测试结果分行、空卡片均符合所选方向。

## 五项视觉检查

- 字体与排版：沿用项目无衬线字体及现有等宽数值字体；桌面标题 24px、手机标题 20px。长名称换行，不使用省略号。路径标签与数值层级清晰。
- 间距与布局：桌面两列、窄屏一列；卡片 16/20px 内边距、16px 网格间距；每条中转单独展示路径和状态。390px 普通及长名称样例的 document scrollWidth 均为 390，控件边界无横向溢出。
- 颜色与 token：使用现有 background/content1/default/primary/success/warning/danger 语义色；配置数量采用中性色；转发状态用颜色与文字共同表达；测速保持已有阈值。
- 图片与图标：基准没有需要生成的照片或插画；使用项目已有 Lucide ArrowRight、Circle、Plus、ChevronRight 图标，未增加依赖或用栅格图代替界面。
- 文案与内容：保留现有页头及 API 内容；将模糊的“落地”副标题改为准确的“所属节点”或“来源”；经测试点的结果标注“估算”，仅测中转路径时不标估算。连接失败直接展示错误。

## 交互与边界检查

- 搜索 `SG.Legend` 返回两张卡片；开启“只看有中转的”后只剩已配置的 SG.Legend.Evo；不存在的名称显示“没有匹配的落地”；清除和筛选恢复正常。
- “管理”打开原有中转管理弹窗；空卡片的“添加中转”打开对应落地的添加表单。
- 点击延迟摘要展开所有 6 个测试点、丢包及分段路径；重测显示检测中并恢复可操作。
- 键盘 Enter 打开详情、聚焦菜单项后 Escape 关闭。
- 临时异常样例覆盖：多条中转、仅中转、长落地及入口名称、已暂停、转发已删除、无测速结果、路径单独测速、长测试点及错误文本。普通和长名称手机截图均已打开检查。
- 补充证据：`latency-details.png`、`mobile-long-name.png`、`mobile-failed-result.png`，均位于上述证据目录。
- 浅色、暗色及手机布局已检查；检查后恢复浏览器默认尺寸。
- 控制台没有 error。观察到项目原有 React Router v7 提示，以及原有管理弹窗缺少 Dialog description 的警告；没有扩展到全站弹窗修复。
- UI 交互使用本地演示数据验证，没有实际创建或删除中转；真实代理连通性不属于本次卡片展示验证。

## 接受的范围差异与后续细节

- 使用现有侧栏、页头、页签和主题，不重建整页。
- 空卡片只在右上角保留一个添加按钮，避免生成图中重复的同名操作。
- 所属节点使用真实字段，区别于生成图里重复落地名称的副标题。
- 已配置落地优先显示，同组内保留原有顺序。
- [P3] 路径名称采用项目的 16px 正文字号，数量标签采用 12px；相对生成图略紧凑，符合真实管理界面的密度。

## 验证结果

- `pnpm run build`：通过（含 TypeScript）。
- `pnpm exec eslint .`：通过，无输出。
- `git diff --check`：通过。
- 未新增前端测试框架或测试文件。
- 实现清单：路径卡片、延迟摘要、准确状态、空卡片、长名称、手机适配、原有弹窗与详情入口，均已完成。


## 2026-10-22 自动同步与质量卡片补充

- 节点页、线路页：后台离线 → 恢复后自动更新；搜索、筛选保留；节点测试点弹层打开时也自动更新。
- 质量卡片：两组标题、刻度、数值基线完全一致；目标地址放入公共页脚，收紧头部空白。
- 1280 px 双列、390 px 单列均已浏览器检查；手机 scrollWidth = clientWidth = 390，无横向溢出。
- 手机进入质量详情、返回列表正常；新增键盘入口。
- 与用户截图并排比较：quality-comparison.png。
- 证据：/Users/rawvix/.codex/visualizations/2026/10/21/01a15f0f-97a4-7111-9536-9c6fcce379c1/relay-cards/auto-sync-quality-qa.json。
- 前端 build、全量 ESLint；后端全量测试、vet、重连并发 race 测试均通过。
- 未解决项：无（本次范围）。
