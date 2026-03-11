# AGENTS.md

## Purpose

本文件定义本仓库中 AI/自动化 Agent 的协作边界、执行顺序与验收标准。

## Project Context

- 项目类型：Golang RESTful API（Gin）
- 鉴权方式：Keycloak OIDC Bearer Token 验签
- 鉴权边界：仅 admin 相关 API 需要鉴权，其他 GET 接口默认公开
- 查询策略：
  - `cards` by-id：Redis 哈希缓存
  - `cards` name 模糊：内存索引（当前字段为 `prefix`）
  - `cards` 列表分页：按真实数据顺序（数组 index）分页，不依赖 id 连续性
- 数据库策略：
  - 默认：development 使用 SQLite，test / production 使用 PostgreSQL
  - 可选覆盖：通过 `DATABASE_DRIVER`（`sqlite` / `pgx`）覆盖默认行为
- Migration 策略：使用 Goose SQL migration，启动自动迁移
- 本地依赖编排：`deploy/compose/test-compose.yaml`（PostgreSQL 18、Redis 8、Keycloak）

## Agent Roles

### 1) API Agent

负责 HTTP 路由、handler、中间件和响应结构。

- 变更范围：`cmd/api`、`internal/transport/http`
- 必须保持：
  - 路由前缀 `/api/v1`
  - 统一错误响应格式
  - 保护接口仅通过 Keycloak Bearer Token 验证
  - 非 admin GET API 不应挂载鉴权中间件
  - admin dashboard 提供独立登录页（不引入本地账号密码体系）
  - `cards` 查询接口保持专用化（避免回退到通用 entity 查询接口）
  - SSE 通知接口 `GET /api/v1/master-data/events` 可用于同步完成事件推送

### 2) Auth Agent

负责 Keycloak/OIDC 相关逻辑。

- 变更范围：`internal/auth`、认证中间件
- 必须保持：
  - 不引入本地用户名密码登录逻辑
  - 不将 Keycloak 密钥硬编码到代码
  - 通过配置项控制 issuer/audience 校验策略

### 3) Data Agent

负责数据库连接、方言兼容和数据访问层。

- 变更范围：`internal/config`、`internal/storage`、仓储层
- 必须保持：
  - 默认规则：`APP_ENV=development` 使用 SQLite，`APP_ENV in {test, production}` 使用 PostgreSQL
  - 当 `DATABASE_DRIVER` 显式设置为 `sqlite` 或 `pgx` 时，以该值为准
  - 不破坏现有配置项名称
  - Redis 中保存 by-id 与顺序索引（分页顺序来源）
  - `cards` 基础信息与 params 分离接口的字段约束保持稳定

### 4) Environment Agent

负责 devcontainer、compose、脚本与开发体验。

- 变更范围：`.devcontainer`、`deploy/compose`、`scripts`、`Makefile`
- 必须保持：
  - 支持通过宿主机容器引擎（Docker API + docker compose 语义）运行测试依赖
  - 命令幂等（重复执行可恢复）

## Execution Protocol

1. 先读取受影响文件再修改，避免覆盖用户手工变更。
2. 优先小步修改，不做与任务无关的重构。
3. 每次改动后至少执行：
   - 在 devcontainer 环境内执行 `go test ./...`
   - 若当前为宿主机非 devcontainer 环境，`go test ./...` 不作为必需验收项；如未执行，需在交付说明中明确
   - 若涉及数据库结构变更，补充 migration 文件，不在业务代码中直接做 DDL
4. 若改动涉及 compose 或脚本，同时检查：
  - `make dev-env-up`
  - `make dev-env-down`
5. 对于失败：仅修复与本次任务直接相关的问题。

## Security & Compliance Rules

- 禁止提交真实密钥、密码、token。
- 示例凭据仅用于本地开发并在文档中明确标注。
- 不在日志中输出完整 Bearer Token。

## Definition of Done

当以下条件满足时任务才算完成：

- 在 devcontainer 环境内工作时，代码通过 `go test ./...`
- 若当前为宿主机非 devcontainer 环境，可不以 `go test ./...` 作为完成条件，但需在交付说明中明确
- 文档（README / env 示例）与实际行为一致
- 新增配置项写入 `.env.example`
- 变更范围聚焦且可回滚

## Preferred Commands

- 运行 API：`make run`
- 单元测试：`make test`
- 迁移升级：`make migrate-up`
- 迁移回滚：`make migrate-down`
- 启动依赖：`make dev-env-up`（兼容旧命令 `make test-env-up`）
- 停止依赖：`make dev-env-down`（兼容旧命令 `make test-env-down`）
- Keycloak token：`make keycloak-token`
