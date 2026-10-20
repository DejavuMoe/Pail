# Pail 视觉与交互规范

视觉方案名为 "Graphite & Paper"。颜色 token 的唯一来源是 `cmd/pail-desktop/theme.go`，视图文件禁止硬编码颜色。

## 原则

1. **中性色承担界面。** 暖纸色与石墨色构成层次：侧栏 `Sidebar` < 主区 `Background` < 浮起的 `Surface`（输入框、卡片、对话框）。层次靠明度差和 1px hairline 区分，不靠阴影与色块。
2. **颜色是信号，不是装饰。** 强调色只用在五处：选中行（`Selection`）、焦点环（`Focus`）、进度与激活指示（`Accent`）、文件夹图标（`FolderIcon`）、主按钮。危险色只用于破坏性动作和失败状态。其余图标一律单色：`TextMuted`，次要为 `TextSubtle`。
3. **激活态用中性填充。** 侧栏选中项、工具栏开关、分段控件的当前项用 `ActiveFill` 或 `SurfacePressed`，文字用 `Text`、字重 500。不用蓝色胶囊。
4. **字重只用 400 / 500 / 600。** 层次靠字号与灰度，不靠粗体。字号：13 正文、12 次要、11 分组标签、15 标题。
5. **间距基数为 4**，常用 4 / 8 / 12 / 16 / 20 / 24。控件高 28，表格行高 30，圆角 6（控件）、8（面板）、10（对话框）。
6. **界面里不放解释性段落。** 必要的提示放 Tooltip 或空状态。

## 状态

中性色填充之间的对比只有约 1.2:1，单靠填充无法区分状态。所以**选中、开启、当前这三种状态，至少要用两种线索表达**：

| 控件 | 填充 | 第二线索 |
| --- | --- | --- |
| 分段控件 | 轨道 `Track` 内凹并带 1px `Border`；当前段用 `Raised` | 当前段加 1px `BorderStrong` 和阴影 `Shadow`，文字用 `Text`、字重 600；其余段用 `TextMuted`、字重 500 |
| 侧栏选中项 | `ActiveFill` | 左侧 2px `Accent` 指示条（高 16，垂直居中），图标用 `Accent`，名称字重 600 |
| 工具栏开关（检查器、传输面板） | `ActiveFill` | 加 1px `BorderStrong` 描边，图标用 `Text` |
| 检查器标签、传输筛选 | 无 | 2px `Accent` 下划线；当前项用 `Text`、字重 600，其余用 `TextMuted` |
| 开关 | 关闭时轨道 `ControlOff`，开启时 `Accent` | 白色圆钮，位置随状态变化 |
| 复选框 | 未选时 1.5px `BorderStrong` 边框；选中时 `Accent` 填充 | 白色对勾 |
| 表格选中行 | `Selection` | 若有焦点行，焦点行另加 1px `Focus` 内描边 |

悬停只改填充（`SurfaceHover`），不能与选中状态混淆。禁用状态用透明度 0.4，同时不响应悬停。

## 对比度（`cmd/pail-desktop/theme_test.go` 强制检查）

- 必要文字（正文、字段标签、表头、按钮文字）≥ 4.5:1：使用 `Text` 或 `TextMuted`。**字段标签使用 `TextMuted`、字重 500**，不用 `TextSubtle`，以免标签被看成占位符。
- `TextSubtle` 只用于占位符、计数、分隔符和次要时间，对比度 ≥ 3.5:1。
- 输入框与次要按钮的描边 `BorderStrong` ≥ 1.8:1；开关关闭时的轨道 ≥ 2:1。
- 状态填充 ≥ 1.17:1，并且必须同时有第二线索。

## 滚动

- mygo 的滚动条浮在内容之上。凡是可滚动区域，**都必须给滚动条留出内边距槽**：内容右侧内边距 ≥ 滚动条宽度 + 6，并用 `ScrollbarInsets` 把滚动条放进这个槽里。滚动条不能盖住输入框、按钮或文字。
- 对话框结构：固定的标题、可滚动的正文、固定的底部按钮。正文可以滚动时，顶部和底部显示 1px `Border` 分隔线；对话框最大高度为窗口高度减 64。在最小窗口 960×600 下，所有控件必须都能操作。
- 列表和面板在最小窗口下不能出现水平溢出；长文本单行截断，并带 Tooltip。

## 窗口外观

- **标题栏与应用融为一体**：所有平台都用 `TitleBarStyle: hidden`（macOS 用 `hiddenInset`），并设置 `TitleBarHeight: 44`，让系统窗口按钮与 44px 的工具栏同高。
  - 侧栏从窗口顶边开始，顶部 44px 是账户切换器。macOS 上它左侧让出红绿灯的位置（`c.TitleBar().Left`）。
  - 右侧主区顶部是一条横贯主区的 44px 工具栏，检查器在它下方。工具栏右侧让出系统按钮的位置（`c.TitleBar().Right`）。
  - 工具栏和账户切换器里的空白区域可以拖动窗口，双击可以最大化。
- **外观同步**：应用主题（跟随系统 / 亮 / 暗）同时设置 `mygo.Theme.SetSource`，让系统按钮、原生弹出菜单和对话框与应用一致。窗口 `BackgroundColor` 使用 `light-dark(...)`，避免启动时闪色。
- **不用传统菜单栏**：Windows 与 Linux 不调用 `App.SetMenu`。macOS 保留系统菜单栏（这是平台约定），只放标准项和命令表。
  - 所有命令通过三个入口找到：
    1. 工具栏上的按钮；
    2. 右键菜单；
    3. **命令面板**（`Ctrl/⌘+K`，也可用 `Ctrl/⌘+Shift+P`）：一个搜索框，列出命令表里的全部命令，显示快捷键和当前是否可用，支持中英文关键字。
  - 工具栏最右侧（系统按钮左边）有一个 `⋯` 菜单按钮，分组列出：命令面板、设置、主题、语言、键盘快捷键、文档、关于 Pail、退出。
- "关于"对话框：图标、版本、许可证与第三方声明（Lucide ISC、Inter OFL），以及项目链接。

## 文案与本地化

- 支持英文和简体中文，默认跟随系统，找不到匹配的语言时回退英文。语言在 设置 → 外观 中选择：跟随系统 / English / 简体中文。
  - 每个用户可见的界面字符串都放进消息目录，不在视图代码里写死。使用 mygo `i18n` 目录：`cmd/pail-desktop/locales/en.json` 是源语言，`zh-Hans.json` 是译文，采用 ICU MessageFormat 复数写法。
- 中文用语统一：

  | 英文 | 中文 |
  | --- | --- |
  | Bucket | 存储桶 |
  | Object | 对象 |
  | Folder / Prefix | 文件夹 / 前缀 |
  | Upload / Download | 上传 / 下载 |
  | Storage class | 存储类别 |
  | Encryption | 加密 |
  | Signed URL | 预签名 URL |
  | Version | 版本 |
  | Delete marker | 删除标记 |
  | Sync | 同步 |
  | Conflict | 冲突 |
  | Endpoint | 端点 |
  | Region | 区域 |
  | Path-style | 路径样式 |
  | Access key | 访问密钥 |
  | Secret key | 私有密钥 |
  | Vault | 加密保险库 |
  | Master password | 主密码 |
  | Transfers | 传输 |
  | Inspector | 检查器 |

- 中文使用全角标点；中文与英文、数字之间留一个空格（例如"3 个对象"）；按钮用动词短语（"上传文件""保存"）；不出现"您"。
- 服务端返回的错误详情保留原文，前面加一句本地化的概括（例如"访问被拒绝："）。
- **"?" 说明图标**：所有含义不直观的配置项，在标签右侧放一个 14px 的 `circle-help` 图标（颜色 `TextSubtle`，悬停时 `TextMuted`），悬停或获得焦点时显示 Tooltip。
  - 说明控制在 1–2 句，讲清楚"它做什么、什么时候需要改"，中英文都有。
  - 例如 Path-style："用 `端点/桶名/对象` 形式访问。MinIO、自建网关通常需要开启；AWS 保持关闭。"

## 组件

### 侧栏

- 背景用 `Sidebar`；与主区之间只有 1px `Border`，侧栏内部没有边框。
- 顶部是账户切换器，高 44：
  - 左侧 24px 圆角方块，`ActiveFill` 底色，内含账户名首字母，12px、字重 600；
  - 中间账户名 13px / 500，下一行 region，11px `TextSubtle`；
  - 右侧 chevron 图标，`TextSubtle`。
  - 整体是一个可点击区域，悬停时 `SurfaceHover`。
  - region 只出现在账户名下一行，不在别处重复。
- 分组标签（BUCKETS、TOOLS）用 `sectionLabel`，左右内边距 16，上 16、下 6。数量跟在标签后，用 `TextSubtle`。
- 列表项：
  - 高 28，左右外边距 8，内边距 8，圆角 6；
  - 14px 图标用 `TextMuted`，名称 13px 用 `Text`，region 11px 用 `TextSubtle` 靠右；
  - 悬停 `SurfaceHover`；
  - 选中项用 `ActiveFill`，名称字重 500，图标换成 `Accent`。
- 桶筛选框高 28：`Surface` 底色、1px `BorderStrong`、圆角 6，占位文字用 `TextSubtle`，左右外边距 12。
- TOOLS 分组贴底；与 BUCKETS 之间是弹性空白，不用分隔色块。

### 工具栏（高 44，`Background`，底部 1px `Border`）

- 按钮一律 ghost 风格：无底色无边框，高 28，左右内边距 8，图标 14px 用 `TextMuted`，标签 13px 用 `Text`；悬停 `SurfaceHover`，按下 `SurfacePressed`；禁用时整体透明度 0.4。
- 分组之间用 16px 高的竖线 `vline`，左右各留 6。
- 右侧检查器、传输面板两个开关：仅图标；打开时用 `ActiveFill`，图标用 `Text`。

### 路径栏（高 40）

- 面包屑：
  - 前面的段 13px，用 `TextMuted`，悬停变 `Text`；
  - 最后一段用 `Text`、字重 600；
  - 分隔符是 chevron-right 12px，`TextSubtle`；
  - 桶段前放 14px 桶图标。
- 右侧搜索框宽 240，样式同桶筛选框。

### 对象表

- 背景 `Background`，没有外框。
- 表头高 28：12px、字重 500、`TextMuted`，无底色，下方 1px `Border`。可排序列的箭头用 `TextSubtle`。
- 行高 30，行与行之间不画分隔线。悬停 `SurfaceHover`，选中 `Selection`，选中行文字保持 `Text`。
- 名称列：16px 图标后接 13px `Text`。文件夹图标用 `FolderIcon`，文件图标用 `TextSubtle`。
- 大小、类型、修改时间、存储类别四列用 12px `TextMuted`；大小右对齐。
- 拖放上传：表格区域整体 1.5px `Accent` 描边、圆角 8，填充 `Selection` 的 50% 透明度，中央是 upload 图标加"Drop to upload to <prefix>"。
- 空状态：垂直居中，32px 图标用 `TextSubtle`，标题 13px / 500，副标题 12px `TextMuted`。

### 检查器（宽 320，`Background`，左侧 1px `Border`）

- 顶部标签条高 40：
  - 图标按钮 28×28，未激活时图标 `TextMuted`；
  - 激活项的图标与文字都用 `Text`，正下方是 2px `Accent` 指示线（宽同按钮，圆角 1）；
  - 不使用填充胶囊。
- 头部，内边距 16：
  - 40px 方形图标块，`Sidebar` 底、圆角 8；
  - 名称 14px / 600 单行省略，副标题 12px `TextMuted`。
- 属性列表：
  - 不画格子表，每行为标签（12px `TextSubtle`，宽 96）加值（13px `Text`），行距 8；
  - 分组之间留 16 间距，加 hairline 和分组标签；
  - 长值（ETag、Key、URI、URL）独占一行：标签单独一行，值用 12px 等宽字体可换行，右上角放复制按钮 `iconButton`。

### 传输面板

- 默认高 168：标题栏 32 加 4 行。最小 96，可折叠。背景 `Background`，顶部 1px `Border`。
- 标题栏：
  - "Transfers" 12px / 600；
  - 筛选项是文字标签"Active 3 / Completed 1 / Failed 1"，12px `TextMuted`；当前项用 `Text`，下方 2px `Accent` 指示线；
  - 右侧是"Clear finished"文字按钮和折叠按钮。
- 行高 32，从左到右依次为：
  - 方向图标，14px `TextMuted`；
  - 名称，13px 弹性宽度；
  - 进度条：固定宽 160、高 4、圆角 2，轨道 `Border`，填充 `Accent`，失败时整条为 `Danger`；
  - 字节数：右对齐宽 128，12px `TextMuted`，例如"1.6 GB of 2.4 GB"；
  - 速度与剩余时间：宽 112，12px `TextSubtle`；
  - 取消或重试按钮 `iconButton`。
- 状态图标：完成用 check `Success`，失败用 alert-circle `Danger`，排队中显示"Queued"，`TextSubtle`。

### 状态栏（高 24，`Sidebar`，顶部 1px `Border`）

- 12px `TextMuted`，各段之间用 `·` 分隔，分隔符为 `TextSubtle`。
- 右侧传输摘要用 arrow-up / arrow-down 12px 图标加数字，不用 Unicode 箭头字符。

### 对话框

- 遮罩用 `Scrim`。
- 卡片：`Surface` 底，圆角 10，宽 440，内边距 20。亮色无边框，带柔和阴影（0 12 32，约 18% 黑）；暗色用 1px `BorderStrong`。只有一层卡片，不要嵌套边框。
- 标题 15px / 600。
- 范围信息用两列网格：标签 12px `TextSubtle`，值 13px `Text`。
- key 列表块：`Sidebar` 底，圆角 6，内边距 8 / 10，12px 等宽字体，最多 5 行，之后接"and N more"。
- 汇总行写成"3 objects · 18.3 KB"，13px / 500。不可撤销提示用 12px `TextMuted`。
- 底部按钮右对齐、间距 8：
  - 次要按钮：`Surface` 底，1px `BorderStrong`；
  - 主按钮：`Accent` 底，`AccentText` 字；
  - 危险按钮：`Danger` 底，`DangerText` 字。
  - 三种按钮高 28，圆角 6。

### 按钮总则

只有三种按钮：ghost、次要（描边）、主要或危险（实底）。整个窗口同一时间最多只有一个实底按钮。
