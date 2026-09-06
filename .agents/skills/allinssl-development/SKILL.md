---
name: allinssl-development
description: "在 AllinSSL Fork 内开展 Go 后端、Vue 前端或部署二次开发，遵循项目分支、源码边界和分阶段设计；不用于其他仓库。"
---

# AllinSSL 二次开发

先读取仓库根 AGENTS.md，再读取与当前任务相关的 docs/development 文档。核对 actual cwd、git status、当前分支和基线。此仓库与 ChatGPT sources/ 同步镜像是不同位置。

已知源码基线是 upstream/1.1.3 的 73cbcb8a213d959e772fb8ab3120abb9efa476c4；这是记录的基线，不是永久“最新版本”。同步前验证上游实际源码分支及 tree，保留 custom 定制历史。

新增能力优先独立目录，少量接入点须有理由：backend/route、server、migrations 和 frontend 菜单/API。先追踪实际鉴权、响应格式、存储和启动方式，不假设已有 RBAC、用户 ID 或新模块。

功能变更先理解相关分析/设计及验收条件，已有有效设计不重复走全套；用户请求编码后按小增量实现并验证。缺陷修复先复现，保证至少一个相关回归证据。只读问题不顺带改代码。

遵循 go.mod、现有前端包管理锁文件和 CI 的真实命令；测试可能初始化数据库或访问 DNS 时先确认隔离条件。不要在仓库真实 data 目录运行会触发初始化/迁移的测试。

DNSControl 任务按同目录的 dnscontrol-integration Skill 进一步约束。使用 Superpowers 中适用的计划、测试、调试和评审流程；缺少该插件时使用本 Skill 和设计继续，不把安装作为普通开发阻塞。

不因 Skill 自动切换模型、创建新任务或开启多代理；需要并行时遵循当前会话已授予的能力和约束。完成时报告实现内容、测试结果与剩余限制，保留无关改动，不自动推送或发布。
