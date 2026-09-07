# AllinSSL Fork 开发约定

## 仓库与基线

本仓库是 mysStack/allinssl 的长期二次开发仓库。origin 为用户 Fork，upstream 为 allinssl/allinssl；操作前以 git remote -v 实际结果核实。

已记录开发基线为 upstream/1.1.3，commit 73cbcb8a213d959e772fb8ab3120abb9efa476c4。不要假设官方 main 长期承载源码：同步前检查实际发布分支、远端 HEAD 和文件树。main 保留已评估上游基线，custom 承载长期定制，feature/* 承载功能开发；不要直接在 main 编写定制代码。

保留已有工作区改动与有效开发提交。分析/检查默认只读；同步、重建历史、push、PR 和发布按用户实际授权执行，不因旧文档存在命令示例就执行。不得向 upstream 推送。

## 开发流程

先理解源码和相关设计，再按用户要求实施；复用已有有效分析，避免重复文档和无关重构。DNSControl 的分析、R1 设计与评审记录位于 docs/development，新增功能以这些文件的最新版本为依据。设计要求不等于现有实现，必须核对代码。

使用 .agents/skills 中适用的项目 Skill；通用 Skills 和 Superpowers 只为当前任务服务，不强制每个小修改完整走所有流程。用户最新范围与约束优先；不因 Skill 默认扩大到多代理、远端写入或生产变更。

阶段性交付按一个可验证的开发阶段提交一次。每次提交前先完成与该阶段风险相称的验证；提交信息使用详细中文，说明功能范围、关键安全约束和已运行验证。不得提交密钥、凭据、数据库、证书、日志、构建缓存或其他运行时产物；阶段提交不自动授权 push、PR 或发布。

后端为 Go/Gin，前端为 frontend/apps/allin-ssl 下的 Vue/TypeScript。遵循现有依赖与锁文件，优先独立新增模块，最小化证书核心、scheduler、workflow 以及上游接入点的改动。

## DNSControl 边界

业务 DNS 写入由锁定 DNSControl 执行；AliDNS SDK 仅用于读取。原 ACME DNS-01 继续走 lego，必须保护临时 challenge 记录。完整 Zone 纳管、不兼容记录只读、严格计划校验、任务锁及失败核对按最新设计实现。

确认阶段和执行阶段的状态不可混淆。CLI 非零退出不能凭 report 中的 0 corrections 当作成功；计划 hash 与本地锁不消除外部并发窗口；AliDNS 更新可能先删后建。禁止从不明结果自动重试真实 push。

不访问真实凭据/生产 DNS 来代替 mock。任何真实 Provider 测试必须有用户指定的环境范围。DNS 写功能在验收完成和授权开启前保持关闭。

## 验证与交付

优先运行与改动风险相称的回归、构建和检查；从仓库/CI 获取真实命令。Go 测试可能触发初始化，涉及 data 或数据库时使用隔离目录，保留用户数据库和证书。

本地/BIND 验证、Provider mock 和真实 AliDNS 测试分别报告，不互相代替。禁止提交密钥、creds.json、数据库、真实证书或含敏感数据的日志。交付说明实际改动、已运行验证、未验证边界及未提交文件；Git 提交、推送和发布不由本文件自动授权。
