# Prototype-first UI

[English](README.md)

一个通过原型审阅推进 UI 设计与实现的 Agent Skill，配合
[Baoyu-Design](https://github.com/JimLiu/baoyu-design)，串联产品审计、交互原型、
审批和生产实现。

适用于新页面、界面重设计、交互改动及原型与生产界面的同步，覆盖 Web、桌面端、
移动端和浏览器扩展。

## 依赖

- 支持文件读写和命令执行的 AI Agent。
- Git、Python 3.9+ 和 Node.js。
- Baoyu-Design，用于生成原型。
- 浏览器或预览工具，用于视觉与交互审阅。

## 安装

在项目目录运行，并在安装器中选择使用的 Agent：

```sh
npx skills add DejavuMoe/prototype-first-ui --skill prototype-first-ui --copy
npx skills add JimLiu/baoyu-design --skill baoyu-design --copy
```

也可以从 [GitHub Releases](https://github.com/DejavuMoe/prototype-first-ui/releases)
下载技能包，解压到所用 Agent 的技能目录。

## 使用

```text
$prototype-first-ui prototype

为文件管理器设计批量重命名流程，包含修改预览、文件名冲突和取消操作。
完成后展示原型供审阅。
```

审阅通过后：

```text
$prototype-first-ui implement

实现设计提交 <SHA> 中已批准的 designs/file-manager/index.html，
本次只实现批量重命名流程。
```

不支持命名调用的 Agent，可以直接读取安装目录中的 `SKILL.md`。
更多示例：[中文](references/prompts.zh-CN.md) · [English](references/prompts.en.md)。

## 工作流

审计 → 原型 → 审阅 → 批准 → 实现 → 验证。

| 模式 | 用途 |
|---|---|
| `audit` | 记录现有产品能力、接口和状态 |
| `explore` | 比较不同设计方向 |
| `prototype` | 创建或修改交互原型 |
| `approve` | 登记指定原型版本的批准 |
| `implement` | 实现已批准的功能 |
| `sync` | 同步原型与生产界面的差异 |
| `verify` | 检查内容、视觉、交互和无障碍 |
| `bootstrap` | 可选的仓库清理与本地历史重置，包含恢复快照 |

[技能指令](SKILL.md) · [工作流参考](references/workflow.md) ·
[发布指南](RELEASING.md) · [更新日志](CHANGELOG.md)

## 许可证

[MIT](LICENSE)
