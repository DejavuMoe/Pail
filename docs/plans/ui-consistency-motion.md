# 实施方案：浮层视觉统一、动效与布局稳定

状态：阶段 0–2 已实现（2026-10-20，本地提交，未发布）；阶段 3（升级 mygo）暂缓，等上游发布正式 tag 后再评估。已确认（2026-10-20），目标版本 v0.2.0，变更已归入两份 CHANGELOG 的 `[0.2.0]`，仍处于发版准备阶段。范围仅桌面应用 `cmd/pail-desktop`，不涉及网站，不使用原型设计技能；实现按 `docs/design.md` 直接进行，完成后把新增规范并入 `design.md`。

## 1. 问题与根因

### 1.1 检查器"标头"标签只剩"…?"

- 位置：`edit_props.go:374-406` 的 `headerRow` 调用 `kvedit.go:74-136` 的 `editRow`。有说明图标时，标签设成 `Grow(0)`（`kvedit.go:80-95`）。
- 根因：mygo 的 `Grow(n)` 在未设置 basis 时**同时把 basis 设为 0**（`mygo/ui/element.go:750-758`），等同 CSS `flex: 0`。单行文本的 min-content 也是 0，所以标签被冻结在 0 宽：只画出省略号，后面紧跟 `?` 图标，空间全被 `ui.Spacer` 吃掉。
- 影响范围：与窗口宽度、语言无关，所有带说明的标头行都会出现。元数据行没有说明图标，用的是 `Grow(1)`，不受影响。
- 现有测试只断言文字存在（`edit_test.go:62`、`flows_test.go:55`），所以没发现。

### 1.2 拖动调整窗口时布局抖动

应用侧（主要原因）：

1. **用上一帧的尺寸布局。** `Element.Bounds()` 返回上一帧的盒子：
   - `pathbar.go:19-25` 用 `a.pathW` 算搜索框宽度；
   - `objects.go:97,144` 用 `a.tableW` 决定显示哪些列。

   每一步缩放都晚一帧，且没有补帧请求，要等约 1 秒后 mygo 的 settle 帧才"跳"到正确位置。
2. **断点在拖动中来回翻转**，且都没有回滞：
   - 工具栏文字在 680 处隐藏（`shell.go:86-87`、`toolbar.go:12,61,80`）；
   - 路径栏在 560 处变紧凑（`shell.go:91`）；
   - 对象表列在 720 / 560 处增减（`objects.go:23-33`）；
   - 侧栏与检查器在 1100 处一次性从 248→220、320→280（`shell.go:80-85`）。

库侧（mygo v0.5.5，Windows）：

- 每次 `WM_SIZE` 同步重建、布局、绘制一帧，并以 sync 0 加 `DwmFlush` 呈现，缓冲区放大 25%，按拉伸缩放（`d3d11.go:435-484,730-748`）。帧耗时超过一个合成周期时，DWM 会拉伸显示旧帧。
- 标题栏系统按钮是独立的 GDI 分层子窗口，与 DXGI 帧不是原子合成（`titlebar.go:184-212`），可能与工具栏右侧留白差一帧。
- 上游 `main`（未发布）的 #517 waitable swap chain、#519 脏矩形局部重绘、#504/#481/#512 布局缓存可以缓解，但只能减轻，不能根治应用侧的晚一帧问题。

### 1.3 浮层视觉不统一

| 浮层 | 现状 | 与规范的偏差 |
| --- | --- | --- |
| 对话框 `dialogs.go:54-93` | `Surface`、圆角 10、阴影 0 12 32 / 18% | 阴影颜色硬编码 |
| 桶设置对话框 `bucket_settings.go:456-479` | 复制了一份对话框样式，头部内边距不同 | 硬编码阴影；分隔线常驻，与"可滚动时才显示"不一致 |
| 命令面板 `palette.go:223-335` | 行高 32、内边距 10、输入框 15px、当前项用 `Selection` + 色条 | 间距不在 4 基数上；当前项用蓝色选中色而非中性色；不显示命令图标；"不可用"分组不用 `sectionLabel`；Esc 键帽圆角 4 |
| 下拉选择 `form.go:194-220` | 圆角 8、两种主题都有描边、阴影 0 8 24 / 14% | 与对话框阴影、描边规则不一致，颜色硬编码 |
| 右键菜单与 `⋯` 菜单 | **系统原生菜单**（`TrackPopupMenuEx`），Segoe UI、系统灰底、无图标 | 无法用主题 token、Inter、13px、圆角；mygo 不提供样式钩子 |
| Tooltip / Toast | mygo 内置，反色，阴影硬编码在库内 | 圆角 5，不在 6/8/10 体系中 |
| Split 分隔条 | 悬停变 `Accent`（mygo 默认） | 超出强调色允许的五处用途 |

另有：`controls.go:36-37` 也有硬编码颜色；`theme.go` 里的 `Shadow` token 只被分段控件使用；全应用只有开关圆钮一处动画（`controls.go:27`）。

## 2. mygo 版本现状

- 最新发布仍是 **v0.5.5**（2026-10-16，与我们锁定的一致）。`main` 已领先 80 个提交，未打 tag，仍在快速变化。
- **动效引擎 v0.5.5 已完整**，第 1–3 阶段不需要升级：
  - 数值动画：`AnimateTo` / `Spring` / `AnimateColor`；
  - 绘制层动画：`AnimatePose`；
  - 出入场：`Transition(ElementTransition{Enter, Exit, Colors, Match})`；
  - 序列与时间线：`Keyframes` / `Timeline`；
  - 读取系统"减少动态效果"：`c.Preferences().ReduceMotion`；
  - 测试时钟：`ui.Tester`；
  - 窗口缩放本身不触发过渡。
- `main` 上对我们有价值、但未发布的内容：
  - Windows 11 风格自绘原生菜单（#555–557）；
  - 缩放与渲染性能（#517、#519、#504）；
  - 滚轮平滑（#497）。
- `main` 上的风险：flex 行为对齐 Chrome（#482），可能让现有布局细微移位；元素 key 哈希方式改变（#426）。

## 3. 设计决定（待确认的已标出）

1. **浮层统一为一套样式。** 在 `theme.go` 新增 token，所有浮层只走一个样式函数 `overlayPanel(kind)`：
   - `ShadowPopover`：亮色 0 8 24 / 14%，暗色无阴影改用 1px `BorderStrong`；
   - `ShadowDialog`：亮色 0 12 32 / 18%，暗色同上；
   - `RadiusPopover = 8`、`RadiusDialog = 10`、`RadiusKeycap = 4`。键帽是新增的小元素，规范里单列。
2. **列表型浮层的行规范**（命令面板、下拉、自绘菜单共用）：
   - 行高 28，左右内边距 8，圆角 6，14px 图标 `TextMuted`，文字 13px；
   - 快捷键用键帽，12px `TextSubtle`；
   - 键盘光标与鼠标悬停是同一种"高亮"：`SurfaceHover`（像原生菜单），不用蓝色 `Selection`；
   - "当前值"用 `Accent` 对勾加字重 600，满足两种线索；
   - 分组标题统一用 `sectionLabel`。
3. **右键菜单，【待确认 Q2】。** 推荐方案 A：Windows 与 Linux 改为应用内自绘菜单（`PopoverBase` + `AttachTo`），与命令面板共用行组件，可以显示图标、键帽，并有入场动效；macOS 保留系统菜单（平台约定）。
   - 代价：菜单不能超出窗口边界，需要自己做子菜单、键盘导航、首字母跳转和屏幕阅读器语义，并改写约 3 组菜单测试。
4. **动效原则：克制、快速、可关闭。**
   - 时长：快 100ms（悬停颜色）、标准 160ms（浮层出入、指示条）、慢 220ms（面板折叠）；
   - 缓动统一 `CubicBezier(0.2, 0, 0, 1)`，浮层弹簧用 `ui.Snappy`，不用 `Bouncy`，不做装饰性循环；
   - 系统开启"减少动态效果"时只保留颜色淡变；
   - 窗口缩放、虚拟化对象表的滚动与行渲染不加动画。
5. **断点与面板宽度。**
   - 所有宽度断点加 24px 回滞（展开阈值 = 收起阈值 + 24）；
   - 1100 处的侧栏和检查器自动缩窄改为"在最小宽度与默认宽度之间随窗口连续变化"或直接取消，【待确认 Q4】。

## 4. 分阶段实施

每个阶段单独提交，阶段内先修再加测试。

### 阶段 0：缺陷修复（不新增依赖，可先发 v0.1.1）

1. `kvedit.go`：标签不再调用 `Grow`。改为把"标签加 `?`"包进 `Row(...).Grow(1).MinWidth(0)`，标签用 `Shrink(1).MinWidth(0).SingleLine()`，并带 Tooltip（与 `help.go:68-72` 的 `switchRowHelp` 一致），去掉 `ui.Spacer`。
   - 全仓检查其他 `Grow(0)` 或可变 `Grow` 的用法。
   - 新增回归测试：在 320 和 280 宽的检查器下，每个标头标签的布局宽度 ≥ 文本宽度或 ≥ 48，且不是纯省略号。
2. 布局改用当前帧尺寸：
   - 搜索框宽度和可见列由 `c.Size()` 减去已知的侧栏、检查器、分隔条宽度算出（`shell.go:86-91` 已有现成算式），集中到一个 `layoutMetrics` 结构里；
   - 删除 `a.pathW`、`a.tableW` 这类上一帧回读。确实必须读 `Bounds()` 的地方，变化时请求补帧（`c.AnimationFrame()`），不等 settle。
3. 断点回滞：
   - `layoutMetrics` 记住上一次的档位，只在越过"阈值 ± 12"时切换；
   - 新增测试：宽度在阈值附近 ±10 抖动时档位不变；
   - 1100 处的面板宽度按 Q4 的决定处理。
4. Split 分隔条悬停色改为 `BorderStrong`，拖动时改为 `TextSubtle`，不用 `Accent`。
5. 文档：`docs/status.md` 记录修复与验证层级；`CHANGELOG.md` 与 `CHANGELOG.zh-CN.md` 的 `[Unreleased]` 同步写入。

### 阶段 1：浮层统一

1. `theme.go`：新增上述阴影与圆角 token，`theme_test.go` 补对应检查。把 `dialogs.go:92`、`bucket_settings.go:462`、`form.go:196`、`controls.go:36-37` 的硬编码颜色改为 token。
2. 新建 `overlay.go`，提供：
   - `overlayPanel(kind)`：对话框、浮层、菜单三种；
   - `listRow(...)`：图标、文字、副文字、键帽、对勾；
   - `keycap(...)`。
3. 命令面板改用 `listRow`：行高 28、间距按 4 基数、输入框 13px，显示命令图标，"不可用"分组用 `sectionLabel`。快捷键与 Esc 一律用键帽。
4. 下拉选择改用同一套 `overlayPanel` 与 `listRow`。
5. 桶设置对话框改为复用 `dialogFrame`，删除复制的样式；分隔线改为可滚动时才显示。
6. Tooltip 与 Toast 改用 mygo 的 `TooltipBase` / `ToastBase`，套用 `overlayPanel`。圆角归入 6 / 8，阴影走 token。
7. 自绘菜单（若 Q2 选 A）：
   - 新建 `ctxmenu.go`，接口对齐现有菜单构造（`Item` / `Checked` / `Disabled` / `Shortcut` / `Separator` / `Submenu`），调用处只换实现；
   - 键盘支持：↑↓、Home/End、Enter、Esc、→ 展开子菜单、← 返回、首字母跳转；
   - 定位：用 `AttachTo` 在光标处按窗口边界翻转；点击外部关闭；
   - macOS 继续走 `ui.Menu`；
   - 菜单测试改为对自绘菜单查询，保留 macOS 原生路径的测试。

### 阶段 2：动效

新建 `motion.go`，集中定义时长、缓动、弹簧和 `reduced(c)`，视图里不出现裸数字。

| 位置 | 动效 | 说明 |
| --- | --- | --- |
| 所有 ghost / 次要 / 主按钮、图标按钮、侧栏项、面板与菜单行 | 填充色淡变 100ms（`Transition{Colors:true}`） | 对象表行由 mygo 绘制且已虚拟化，不加 |
| 对话框、命令面板 | 遮罩淡入 160ms；卡片淡入加 `Scale 0.98→1`、`Y 4→0`；退出 100ms 淡出 | 用 `DialogBase` + `ElementTransition{Enter, Exit}` |
| 下拉、自绘菜单、Tooltip | 淡入加 `Scale 0.97→1`，`TransformOrigin` 取锚点方向，120ms；退出 80ms | Tooltip 只淡入 |
| 检查器标签、传输筛选的 2px 指示线 | 在标签间滑动 160ms | `Match` 共享元素或对 X / 宽度做 `AnimateTo` |
| 侧栏选中指示条 | 在项间滑动 160ms | 同上 |
| 传输面板折叠 / 展开 | 高度 220ms | 与 mygo `Collapsible` 同法 |
| 检查器打开 / 关闭 | 内容淡入 160ms；宽度不动画 | 避免拖动分隔条时与 Split 冲突 |
| 进度条 | 宽度 `AnimateTo` 160ms，失败时颜色淡变 | 每秒最多 10 次事件，不累积 |
| 状态栏与传输计数 | `ContentTransition{Numeric}` | |
| 设置页"已保存"提示 | 淡入淡出替代闪现 | |

开启"减少动态效果"时：位移、缩放、滑动直接到位，只保留颜色与透明度淡变（mygo 默认行为）。新增测试用 `ui.Tester` 的受控时钟，断言：动画结束态正确；减少动态效果时无中间帧；动画进行中仍可点击、可用键盘操作。

### 阶段 3：评估升级 mygo（独立分支，不阻塞前面阶段）

- 首选等待 v0.5.6 等正式 tag；若需要提前验证，在独立分支锁定 `main` 的伪版本。
- 运行全量测试、`PAIL_UI_SNAPSHOT_DIR` 截图（亮暗、中英文、960×600），逐张对比 #482 flex 变化带来的位移。
- Windows 原生实测缩放：对比升级前后 `--smoke-screenshot` 和人工拖动观感。
- 若 Q2 选 B（保留原生菜单），这一阶段的 Win11 自绘菜单就是原生菜单的主要改进来源。
- 升级只在测试与人工审阅都通过后单独提交，并在 `status.md` 记录。

## 5. 规范与文档更新

- `docs/design.md` 新增三节："浮层"（面板、行、键帽、阴影 token）、"菜单"（自绘与 macOS 原生的分工）、"动效"（时长、缓动、哪些地方禁止动画、减少动态效果）。并更新状态表：命令面板与菜单的高亮和当前值线索。
- `docs/architecture.md` §7 测试分层补"动效用受控时钟测试"。
- 新增文案（如菜单无障碍名、键帽读法）进入 `locales/en.json` 与 `zh-Hans.json`。

## 6. 验证

每阶段都跑：

- `gofmt`、`go vet ./...`、`go test ./...`；
- Windows 构建，以及经 `linux-task.ps1` 的 Linux `go vet` / `go test -race` 与构建；
- 推送后 GitHub Actions 六平台。

界面验证：

- 截图审阅：960×600 与 1560×980、亮暗、中英文；
- Windows 原生窗口人工拖动缩放：对比修复前后录屏；
- WSLg 冒烟。

`docs/status.md` 只记录实际完成的层级：headless 测试不等于目视审阅，Windows 原生不等于 macOS。

## 7. 决定（2026-10-20）

- **Q1 版本节奏**：阶段 0–2 全部进入 v0.2.0；只在本地提交，不推送、不发版。
- **Q2 右键菜单**：A。Windows 与 Linux 用应用内自绘菜单，macOS 保留系统菜单。
- **Q3 mygo 升级**：等正式 tag；阶段 3 暂缓。
- **Q4 窄窗口面板宽度**：取消 1100 处的自动缩窄。
- **Q5 动效强度**：比"克制"档更明显，带轻微弹性。时长改为：快 120ms（悬停颜色）、标准 200ms（指示条滑动、浮层出入、进度）、慢 260ms（面板折叠、对话框）。浮层和对话框入场用弹簧 `SpringOptions{Duration: 240ms, Bounce: 0.18}`，起始 `Scale 0.94`、`Y 6`；指示条滑动用 `Bounce: 0.12`。退出不带弹性，用 ease-out 120ms。系统"减少动态效果"时规则不变。
