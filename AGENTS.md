# 项目开发约定

- 源代码唯一位置是 Windows 当前仓库，只在 Windows 做 Git 写操作。
- 主仓库是 Forgejo（`ssh://git@ssh.via.moe/dejavu/Pail.git`），每次推送自动镜像到 GitHub（`github.com/DejavuMoe/Pail`）。GitHub 只读，不在上面提交或合并。
- Linux 编译与测试通过 `linux-task.ps1 -Mode build -Project <仓库根目录> -Command '...'`；同一镜像的 build 任务必须串行。

## 结构
- `cmd/pail-desktop` 桌面界面（mygo）；`cmd/pail` 命令行；`internal/app` 应用编排；`internal/storage` S3 协议层；`internal/cloud` AWS 专有服务（CloudFront）。
- `internal/rules` 上传规则与压缩；`internal/safepath` 下载路径安全；`internal/trash` 本地回收站；`internal/s3test` 内存 S3 测试服务（`--demo` 也用它）。
- `tools/genicons` 图标生成；`tools/release` 跨平台发布构建与验包入口，复用 `tools/pack` 和 `tools/verify-release`；`integration/` MinIO 与外部端点验收。架构与不变量见 `docs/architecture.md`。
- `site/` 与 `tools/site` 是 https://pail.download 的静态网站（Landing Page）；原型在 `designs/pail-site/`，部署流水线在 `.woodpecker/site.yml`，说明见 `docs/site.md`。

## 规则
- 网络和文件传输不能阻塞 UI 线程；界面更新一律经 mygo `Window.Update`。
- 状态必须对应真实完成的操作。验证按 API / 界面 / 协议测试 / 真实端到端四层分别记录在 `docs/status.md`，不合并、不夸大。
- 基础验证：`gofmt`、`go vet ./...`、`go test ./...`、Windows 与 Linux 构建。macOS 交叉编译不代表真机验证。
- 视觉与交互规范见 `docs/design.md`：颜色只来自 `cmd/pail-desktop/theme.go`；选中、开启、当前状态至少两种线索；对比度由 `theme_test.go` 检查；可滚动区域给滚动条留槽；最小窗口 960×600 下所有控件可操作。
- 本地化：每个用户可见的界面字符串都放进 `cmd/pail-desktop/locales/` 的 `en.json` 与 `zh-Hans.json`，经生成的函数调用，视图里不写死文案；中文术语表在 `docs/design.md`。
- 双语文档：`README.md`、`docs/cli.md`、`CHANGELOG.md` 各有中文版（`README.zh-CN.md`、`docs/cli.zh-CN.md`、`CHANGELOG.zh-CN.md`），网站据此生成中英文页面。改英文版时在同一提交里同步中文版，标题层级与顺序保持一一对应。`LICENSE` 只保留英文原文。
- 命名与兼容：产品名 Pail。不得破坏旧版 openS3 的用户数据——配置目录、钥匙串服务名、保险库格式、`opens3-*` 对象 metadata 一律"读旧写新"，不删除用户数据。

## 脚本与 CI
- 脚本不得绑定本机路径：仓库根目录从脚本自身位置解析，输出目录可用参数改写；本机专用工具（如 `linux-task.ps1`）只能是可选分支，Linux 上要能直接运行。
- 测试、CodeQL 与发布在 GitHub Actions，工具版本由 `mise.toml` 锁定，Woodpecker 只部署网站。命令、输入输出和哪些任务不能无人值守运行，见 `docs/ci.md`；改脚本时同步更新它。

## Landing Page 与 prototype-first-ui
- 项目级技能 `prototype-first-ui`（及其依赖 `baoyu-design`）只用于 Landing Page：`designs/pail-site/`、`site/`、`tools/site/`。桌面应用和命令行的界面改动不使用这两个技能，按 `docs/design.md` 直接实现。
- 网站改动先改原型并经用户明确批准，再实现到 `site/`；设计提交与实现提交分开。内容与产品约束见 `docs/product/constraints.md`。
- 网站不加载第三方资源（字体、脚本、统计），配色沿用 `theme.go` 的 Graphite & Paper，中英双语，跟随系统亮暗并可手动切换。

## 云资源安全
- 操作云资源前由应用展示范围并要求明确确认；开发测试只用隔离的本地服务或 httptest。
- 例外：用户提供的真实 S3 兼容测试端点（自托管 VersityGW，桶 `test`）。
  - 凭据只经环境变量 `PAIL_EXT_*` 传入，不写入仓库、产物或日志。
  - 只读写本次运行创建的唯一前缀 `pail-e2e/`，结束时清理。
  - 不改桶级配置，不建桶、不删桶，不写其他桶。

## 品牌与发布
- 图标由 `go run ./tools/genicons` 从 `assets/brand/pail-icon.svg` 生成，不手改生成物。
- 本地与 CI 发布构建统一用 `go run ./tools/release -version X`，不依赖 PowerShell；`scripts/release.ps1` 只保留兼容入口。真实服务端验收用 `scripts/test-integration.ps1` 与 `scripts/test-external.ps1`。
