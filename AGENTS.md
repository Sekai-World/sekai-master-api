# AGENTS.md

## Purpose

本文件定义本仓库中 AI/自动化 Agent 的协作边界、执行顺序与验收标准。

## Project Context

- 项目类型：Golang RESTful API（Gin）
- 鉴权方式：Keycloak OIDC Bearer Token 验签
- 鉴权边界：仅 admin 相关 API 需要鉴权，其他 GET 接口默认公开
- 数据库策略：
  - 默认：development 使用 SQLite，test / production 使用 PostgreSQL
  - 可选覆盖：通过 `DATABASE_DRIVER`（`sqlite` / `pgx`）覆盖默认行为
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

### 4) Environment Agent

负责 devcontainer、compose、脚本与开发体验。

- 变更范围：`.devcontainer`、`deploy/compose`、`scripts`、`Makefile`
- 必须保持：
  - 支持通过宿主机容器引擎（Podman API + docker compose 语义）运行测试依赖
  - 命令幂等（重复执行可恢复）

## Execution Protocol

1. 先读取受影响文件再修改，避免覆盖用户手工变更。
2. 优先小步修改，不做与任务无关的重构。
3. 每次改动后至少执行：
   - `go test ./...`
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

- 代码通过 `go test ./...`
- 文档（README / env 示例）与实际行为一致
- 新增配置项写入 `.env.example`
- 变更范围聚焦且可回滚

## Preferred Commands

- 运行 API：`make run`
- 单元测试：`make test`
- 启动依赖：`make dev-env-up`（兼容旧命令 `make test-env-up`）
- 停止依赖：`make dev-env-down`（兼容旧命令 `make test-env-down`）
- Keycloak token：`make keycloak-token`
