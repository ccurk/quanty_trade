# 策略代码快照（owner 直令 2026-08-05：代码入 git，DB 只留工作集）

- 本目录 = 策略 `8eb182b6-ee74-4125-a602-f0a91f376432`（Meme 合约信号计算引擎）的模板代码档案库。
- 命名：`tpl<template_id>.py` = DB StrategyTemplate 同号代码的逐字节快照。
- 协议：每次 `POST /api/admin/optimize/apply` 成功后，同轮把上线代码以新 tpl 编号提交本目录并 push（cron 推分支 claude/confident-fermi-wx3cei，owner 合并进 main；cron 严禁直推 main）。未提交 = 流程未完成。
- DB 端每策略只保留最新 3 版模板（当前 + 2 步回滚深度），更旧版本由 backend retention 在 apply 后自动清扫（见 claude/dev-template-retention 补丁）；全量历史永在本目录 git 提交史。
- 当前绑定：**tpl534**（2026-08-05，S17 BRAKE禁short gate 移除；S4/S8/S12/S13/S14/S15/S16/E2fix/S18 锚点全保留）。tpl477 = 上一版（rollback 基底，S18 上线版）。
