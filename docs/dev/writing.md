# 写作规范

适用于 `README*.md`、`docs/cli*.md`、`CHANGELOG*.md`、`CONTRIBUTING.md`、`docs/dev/` 和网站文案 `site/locales/`。应用界面文案另见 [design.md](design.md) 的术语表。能自动检查的规则由 `go test ./tools/site/ -run TestCopyRules` 执行。

## 读者

| 文档 | 读者 | 只写 |
| --- | --- | --- |
| `README*.md`、`docs/cli*.md`、网站 | 安装和使用 Pail 的人 | 能做什么、怎么装、怎么用、数据存在哪、已知限制 |
| `CHANGELOG*.md` | 升级的人 | 用户能感知的变化 |
| `CONTRIBUTING.md`、`docs/dev/` | 改代码的人 | 构建、测试、架构、发布、验证记录 |

同一件事只写在一处。用户文档不链接 `docs/dev/`；README 的"从源码构建"只指向 `CONTRIBUTING.md`。

## 句子

- 先写读者能做什么，再写怎么做。
- 用具体的名词、数字和命令，不用形容词撑场面。"14 项存储桶设置"，不写"丰富的设置"。
- 一句一件事。不写口号、比喻、排比三连、反问。
- 限制直接写成限制："AssumeRole 还没有在真实 AWS 账户上测试。"不写"这并不证明……""这不代表……"。
- 少用"从不、总是、所有、绝不"。只有在有测试保证、而且读者需要这个保证时才用。
- 不写开发过程：CI 运行编号、提交 SHA、验收日期、内部包名和函数名、工具版本、像素和 token 数值、与其他产品的比较。
- 宣称的平台和服务以 [status.md](status.md) 的记录为准。

## 禁用词

英文：seamless、powerful、effortless、blazing、robust、cutting-edge、state-of-the-art、game-changing、supercharge、unleash、elevate、delightful、world-class、next-generation、just works。

中文：无缝、强大的、轻松、极致、一站式、赋能、全方位、丝滑、完美、革命性、颠覆、神器。

用户文档里不出现内部标识：`internal/`、`s3test`、`linux-task`、`PAIL_EXT_`、`Woodpecker`、`CodeQL`。

## 中文排版

- 中文与英文、数字、行内代码之间加一个空格：`在 macOS 11 上运行 pail ls`。
- 用全角标点；英文句子内部用半角标点。
- 数字与单位之间加空格：`10 MB`、`1 h`。百分比和度数不加。
- 术语按 [design.md](design.md) 的术语表，例如"存储桶""对象键""预签名链接"。
- 中文版不是逐词翻译：意思一致、标题一一对应，句子按中文习惯写。

## Markdown 与网站渲染

- 行内代码只放短内容，每个不含空格的部分不超过 40 个字符。更长的命令、JSON、URL 放进代码块。网站上行内代码只在空格处换行，过长的部分会撑出页面。
- 表格第一列只放短名称（一个命令、一个参数）。用法和示例写在表格外的代码块里。
- 只用 `##` 和 `###` 标题。中英文版本的标题数量、层级和顺序一一对应，网站生成器会检查。
- 网站展示的 README 小节和 `docs/cli*.md`、`CHANGELOG*.md` 里不放图片和 HTML。
- 改英文文档时，在同一个提交里改中文版。`LICENSE` 只有英文。
