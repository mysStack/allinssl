# Codex 开发 Skills 配置记录

配置日期：2026-09-06。范围：Windows Codex、WSL Ubuntu-26 的 bruce 用户，以及本 AllinSSL 仓库。仅配置开发工作流，没有实施 DNSControl 功能、提交 Git 或操作真实 DNS。

## 已完成安装

Windows 通过现有 Codex CLI 0.153.1 的插件管理命令安装 `superpowers@openai-curated-remote`，版本 `6.3.0`。安装后再次查询确认 `installed=true`、`enabled=true`，插件清单的 hooks 为空。

插件位于 `C:\Users\mys93\.codex\plugins\cache\openai-curated-remote\superpowers\6.3.0`，包含 14 个 Skills：brainstorming、dispatching-parallel-agents、executing-plans、finishing-a-development-branch、receiving-code-review、requesting-code-review、subagent-driven-development、systematic-debugging、test-driven-development、using-git-worktrees、using-superpowers、verification-before-completion、writing-plans、writing-skills。

WSL 未检测到 PATH 下可直接运行的 Codex 命令。本次没有额外安装 Linux CLI；已将同一插件完整副本保存在 `/home/bruce/.local/share/codex-plugins/superpowers/6.3.0`，并建立 `/home/bruce/.agents/skills/superpowers` 指向其 skills 子目录的符号链接，供 WSL 内扫描用户级 Skills 的 Codex 客户端使用。这是本地 Skills 副本，不是声称已在 WSL 插件管理器完成安装。

## 自定义通用 Skills

以下 6 项分别安装在 Windows `C:\Users\mys93\.agents\skills` 与 WSL `/home/bruce/.agents/skills`，默认允许按任务内容选择：

| Skill | 用途 |
| --- | --- |
| repo-analysis | 陌生仓库与二次开发分析，区分设计和已实现代码 |
| upstream-sync | 识别真实源码发布分支，评估并按授权同步上游 |
| github-fork-maintainer | Fork 远端、main/custom/feature 分支与基线维护 |
| docker-debugging | Docker/Compose 构建、启动、网络、挂载与权限排障 |
| kubernetes-debugging | 明确 context/namespace 后定位工作负载、网络与存储故障 |
| release-check | 发布前代码、测试、镜像、迁移及回滚证据检查 |

通用编码、TDD、系统调试、计划和评审由 Superpowers 提供，避免再创建同名或重复流程的 coding/debugging Skills。Go/Gin 和 Vue 的项目差异放在项目 Skill。

## 项目级配置

仓库新增 `AGENTS.md` 保存长期约定，并新增两项 `.agents/skills`：

- `allinssl-development`：AllinSSL 分支、Go/Vue 入口、设计优先和隔离验证。
- `dnscontrol-integration`：业务 DNS 与 ACME 分离、完整快照、预览/执行判定、凭据边界及失败核对。

项目规则指向现有分析、R1 设计和设计评审记录，不重复嵌入整份设计。用户当前任务和已授予范围优先；检查、安装 Skills 或引用历史建议不会自动授权 push、真实 DNS 写入、生产重启或多代理任务。

## 使用与加载

官方插件文档要求安装后开启新的聊天/CLI 会话加载插件能力。独立 Skills 通常自动检测，下个回合可查看是否出现；如果没显示，重启客户端。在本仓库工作目录启动支持本地 Skills 的 Codex，项目级规则才会按仓库范围发现；Windows 的 ChatGPT 同步镜像不是这个 WSL Git 仓库。

示例请求：`使用 repo-analysis 检查当前实现与设计的差距`；`使用 upstream-sync 仅评估上游变化，先不合并`；`使用 allinssl-development 和 dnscontrol-integration 实现第一批只读能力`。在支持的界面可用 @ 或 $ 选择 Skill。

Superpowers 原始内容按安装版本保留，未修改其内部流程。是否委派子代理、执行 Git 操作或运行部署，由当前会话权限和用户任务决定；Skill 名称本身不是执行授权。

## 验证与维护

8 个自定义 Skills 均通过 Skill Creator 的 quick_validate.py；6 个 Windows 副本也分别校验通过。核对了技能触发范围，检查请求保持只读，项目基线记录不被当作永久最新上游，发布检查不等同发布授权。该验证不声称完成真实开发任务的端到端行为测试。

Superpowers 两边的 73 个文件逐一校验 SHA-256 一致，WSL 符号链接解析正确，包含 14 项 Skills。项目配置文件归 bruce 所有。未改变模型、审批策略、Git remote、分支 HEAD 或生产设置。

Windows 插件由官方插件管理更新。WSL 副本固定为 6.3.0，不会随 Windows 缓存自动升级；将来升级需复制经过核对的新版本并更新该符号链接。自定义通用 Skills 当前为两份相同的独立副本，后续修改应同步并重新验证。不要删除正在被 WSL 链接引用的版本目录。

参考：[官方插件使用与安装](https://developers.openai.com/codex/plugins)、[官方本地 Skills 目录与发现规则](https://developers.openai.com/codex/skills)。
