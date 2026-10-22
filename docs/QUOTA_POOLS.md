# 混池额度分配

管理员在「混池额度分配」选择管理员管理的 OpenAI OAuth 账号、建立池并填写车友份额。池可以跨网关分组，每个账号只能属于一个池；保存配置不会修改分组或路由。份额最多三位小数，每池合计不超过 100%，未分配部分保留。

保存合同与同步余额是两个动作。保存立即更新当前周期权益和只读权限；点击「预览分配」后可单独同步余额。拆分继承份额，合并时相同合同保留，不同合同清空并要求重新填写。修改合同会留下版本快照，过时预览不能用于手动同步。

## 容量与余额

- 账号容量仅使用 7 天窗口预测：观测截止时的账号统计成本 ÷ 已用比例。成本口径是 `COALESCE(account_stats_cost, total_cost) × account_rate_multiplier`，与账号统计保持一致。
- 百分比与预测成本采用同一窗口和观测截止点；后续消费只增加已用成本，不放大旧百分比对应的预测。
- 真实 0% 沿用同一上游身份最近有效预测，可以跨周期复用，同时扣除新周期消费。首次没有预测时显示「待估算」。缺快照、未知窗口、过期窗口和读取失败均不会解释为零容量。
- 最近有效预测在账号用量查询和余额同步时持久化。更换上游账号身份会删除历史预测和旧用量快照；已识别身份下的令牌轮换保留预测。
- 用户目标余额为 `max(0, 各池预测容量 × 对应合同份额之和 − 用户在各来源账号当前窗口的账号统计成本之和 − 冻结余额)`。各账号独立处理周期边界，汇总正负权益后才统一归零。
- 任一来源预测不完整时，跳过受影响用户的整笔同步，其他用户可正常同步。

**此功能托管的是拼车专用余额。充值、兑换和人工调整不会增加合同权益，后续同步会覆盖这些余额变化。** 删除最后一份正份额后，立即撤销账号查看权限；下次同步将余额归零一次，然后结束托管。

自动同步默认关闭，默认间隔 5 分钟，范围 5～1440 分钟。后台每分钟检查到期任务，不补发错过的周期。手动和定时运行共用数据库锁和持久化幂等记录，可在页面查看成功、跳过或失败原因。

## 权限与事务

车友在「混池账号」只能查看当前有正份额池内的有效管理员账号，支持本地筛选、5h/7d 用量、重置时间、预测及最近 30 天聚合统计。页面读取不会发起上游查询。响应不含凭证、代理配置、管理员备注、其他用户请求明细；变更为私人账号后也不再可见。

管理员管理的 OpenAI OAuth 标准计费请求，将用量记录和扣费一同写入现有 `UsageBillingRepository.Apply` 事务，并复用请求去重。同步锁定合同配置、账号和用户后重读成本，原子更新余额及同步记录；失败时全部回滚。成功提交后失效余额和鉴权缓存。

迁移 `246_quota_pools.sql` 随正常启动迁移执行，Ent 模型与现有 Wire 生命周期一同注册，不需要新增独立进程。

## 接口

| 范围 | 接口 |
| --- | --- |
| 管理员合同 | `GET /api/v1/admin/quota-pools`、`PUT /api/v1/admin/quota-pools` |
| 预览与同步 | `GET /api/v1/admin/quota-pools/preview`、`POST /api/v1/admin/quota-pools/sync` |
| 定时设置 | `GET/PUT /api/v1/admin/quota-pools/settings` |
| 同步记录 | `GET /api/v1/admin/quota-pools/runs` |
| 车友只读 | `GET /api/v1/pool-accounts`、`GET /api/v1/pool-accounts/:id`、`GET /api/v1/pool-accounts/:id/usage`、`GET /api/v1/pool-accounts/:id/stats` |

保存合同提交 `revision` 和完整 `pools`；手动同步提交预览的 `revision` 和唯一 `operation_key`。失败重试复用同一标识，成功的重复请求只返回原结果。管理员接口继续使用原管理员鉴权。

## 验证

```sh
cd backend
go test -tags=unit ./internal/service ./internal/repository ./internal/server/routes \
  -run 'QuotaPool|UsageBilling|RecordUsage|AccountUsage|ShouldRefreshOpenAI|WindowStats'
```

真实 PostgreSQL 测试通过 `SUB2API_QUOTA_POOL_TEST_DSN` 启用：连接角色须能创建临时数据库，测试会创建独立数据库、应用完整迁移，并在结束时删除该测试数据库。覆盖扣费和用量原子提交、回滚、并发同步、跨实例定时争抢、重复请求、合同版本、独立重置窗口和跨周期 0%。

```sh
cd frontend
pnpm exec vue-tsc --noEmit
pnpm exec vitest run src/views/admin/__tests__/QuotaPoolsView.spec.ts \
  src/views/user/__tests__/PoolAccountsView.spec.ts \
  src/components/account/__tests__/AccountUsageCell.spec.ts src/i18n/__tests__
```
