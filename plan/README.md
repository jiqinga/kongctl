# Kong CLI 开发方案（Go 1.25）

## 目标与范围
- 目标：实现命令行工具，基于 Kong Admin API 与 OpenAPI 文件，幂等创建/更新 Service、Route、Upstream、Target 及常用 Plugins，并支持工作空间（Workspace）与环境隔离。🎯
- 适配：Kong 3.x（Admin API），支持 DB 与 DB-less 模式；支持 `--dry-run`/`--diff`。

## 技术栈与依赖
- 语言/版本：Go 1.25
- CLI：`spf13/cobra`（命令行）+ `spf13/viper`（配置/环境变量）
- Kong 客户端：`github.com/kong/go-kong/kong`（官方 Go 客户端）
- OpenAPI 解析：`github.com/getkin/kin-openapi`（OAS3）
- 日志：标准库 `log/slog`；测试：`go test` + `httptest`；Lint：`golangci-lint` ✅

## 目录结构
- `cmd/kongctl/main.go`：入口与根命令
- `internal/cli/`：子命令装配（service/route/upstream/plugin/openapi）
- `internal/config/`：配置加载（flags > env > file），`~/.kongctl/config.yaml`
- `internal/kong/`：Kong Admin API 封装（客户端、重试、鉴权）
- `internal/apply/`：diff/plan/apply（幂等 Upsert）
- `examples/`：示例 OpenAPI 与命令

## 配置与鉴权
- 优先级：命令行参数 > 环境变量 > 配置文件
- 关键项：`--admin-url`/`KONG_ADMIN_URL`、`--token`/`KONG_ADMIN_TOKEN`、`--tls-skip-verify`
- 说明：当前场景未启用 Workspace，工具默认不设置 Workspace 相关头或路径。

## 命令设计（示例）
- 基础
  - `kongctl configure --admin-url https://admin:8001 --token $TOKEN --workspace dev`
  - `kongctl ping`（连通性自检）🏓
- Service/Route
  - `kongctl service sync --name api --url http://backend:8080`（默认自动创建 Upstream=`api-upstream`，并添加 target=backend:8080；可用 `--upstream` 覆盖，`--auto-upstream=false` 关闭）
  - `kongctl route sync --service api --paths /v1 --methods GET,POST --hosts api.example.com`
- Upstream/Target
  - `kongctl upstream sync --name api-upstream`
  - `kongctl target add --upstream api-upstream --target backend:8080 --weight 100`
- Plugin
  - `kongctl plugin upsert --scope service:api --name rate-limiting --config minute=100 policy=local`
\

## 说明
- 已确认当前工具不使用 OpenAPI 指令，相关功能与文档已移除。

## 幂等与安全
- 资源查找优先使用唯一名称（Name）进行 Upsert；变更前生成 diff；支持 `--dry-run`。🔒
- 授权：Admin Token 通过 `Authorization` 或 `Kong-Admin-Token` 头传递；避免日志泄露敏感信息。

## 迭代里程碑
1) 项目初始化：Cobra/Viper、配置与 `ping` 命令
2) Service/Route `sync`（含 `--dry-run`/`--diff`）
3) Upstream/Target 管理
4) Plugins 最小支持（限常用：rate-limiting、cors、jwt 等）
5) CI 与发布：`golangci-lint`、`go test`、交叉编译（`goreleaser` 可选）🚀
