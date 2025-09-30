# kongctl

> 轻量级、幂等的 Kong Admin API 管理命令行工具：一份 YAML / JSON 即可批量创建或更新 Service / Route / Upstream / Target，并支持“路由简写”自动生成上游与实例。内置 dry-run 计划与字段级 diff，帮助你安全演练与审阅变更。

---

## ✨ 特性概览
- **幂等同步**：统一 `create or update` 语义；默认只创建缺失资源，启用 `--overwrite` 可安全更新差异。
- **一份文件批量管理**：`kongctl apply -f xxx.yaml`，支持 `upstreams / services / routes` 三层结构，或纯 `routes` 简写。
- **路由简写 (auto mode)**：只写 Route + Backend，自动派生 `<name>-service` 与 `<name>-upstream` 并挂载 Targets。
- **Dry-Run 计划**：`--dry-run` 展示拟执行的精简计划；配合 `--diff` 输出字段级差异；`--compact` 隐藏无变化项。
- **可读输出**：中文 + Emoji（可用 `--no-color` / `--ascii` 关闭颜色和 Unicode）。
- **局部覆盖策略**：默认避免破坏现有配置；需要修改时显式加 `--overwrite`。
- **服务扩展字段**：支持 `retries / connect_timeout / read_timeout / write_timeout` 差异识别与补丁更新。
- **多解析模式**：顶层对象、列表（routes 简写）、或单 Route 对象均可被自动识别。
- **最小封装客户端**：`internal/kong` 直接贴近 Admin API，方便扩展更多资源类型。

---

## 🧱 目录结构
| 目录 | 说明 |
|------|------|
| `cmd/kongctl/` | 程序入口 `main.go` |
| `internal/cli/` | Cobra 子命令实现（apply / init / ping / service / route / upstream / target / completion） |
| `internal/kong/` | 访问 Kong Admin API 的最小客户端封装 |
| `internal/apply/` | Dry-run 计划模型与渲染逻辑 |
| `internal/config/` | 基于 Viper 的配置加载与视图 |
| `examples/` | 示例 YAML（含路由简写示例） |
| `Makefile` | 常用开发任务（build / test / tidy 等） |

---

## 🚀 安装 / 构建
环境要求：Go 1.25+

### 方式一：本地编译
```bash
# 克隆仓库
git clone <your-repo-url> && cd kong

# 编译
make build   # 生成 bin/kongctl

# 或直接
go build -o bin/kongctl ./cmd/kongctl
```

### 方式二：临时运行
```bash
go run ./cmd/kongctl --help
```

> 依赖下载失败可尝试：`make tidy`

---

## ⚙️ 初始化与全局配置
首次使用可执行：
```bash
kongctl init --admin-url http://localhost:8001 --token <KONG_ADMIN_TOKEN>
```
会写入：`~/.kongctl/config.yaml`。

也可直接使用环境变量（优先级：flag > env > file）：
```
KONGCTL_ADMIN_URL=http://localhost:8001
KONGCTL_TOKEN=xxx            # 可选
KONGCTL_WORKSPACE=default     # 可选
```
常用全局 flags：
| Flag | 说明 |
|------|------|
| `--config` | 指定配置文件（默认 `~/.kongctl/config.yaml`） |
| `--admin-url` | Kong Admin API 地址（必需） |
| `--token` | 管理 Token（可选，RBAC 环境使用） |
| `--workspace` | 指定 Workspace（可选） |
| `--tls-skip-verify` | 跳过 TLS 证书校验（仅测试/非生产环境） |
| `--no-color` | 关闭彩色输出（或设置环境变量 `NO_COLOR=1`） |

---

## 📦 核心命令速览
| 命令 | 用途 | 示例 |
|------|------|------|
| `kongctl ping` | 健康探测 | `kongctl ping` |
| `kongctl service sync` | 创建/更新单个 Service | `kongctl service sync --name echo --url http://httpbin.org` |
| `kongctl route sync` | 创建/更新单个 Route | `kongctl route sync --service echo --paths /v1/users --methods GET` |
| `kongctl upstream sync` | 创建 Upstream | `kongctl upstream sync --name user-up` |
| `kongctl target add` | 给 Upstream 添加 Target | `kongctl target add --upstream user-up --target svc-1:8080 --weight 100` |
| `kongctl apply` | 批量幂等同步 | `kongctl apply -f examples/route-simple.yaml` |
| `kongctl apply example` | 生成示例模板 | `kongctl apply example --type route-simple -o my.yaml` |
| `kongctl completion` | 生成 Shell 补全脚本 | `kongctl completion bash` |

完整帮助：`kongctl --help` 或子命令 `--help`。

---

## 🗂️ Apply 文件格式
支持三种顶层结构：
1. 对象：`{ upstreams: [...], services: [...], routes: [...] }`
2. 纯列表：`[ {route1}, {route2} ]`（视为“路由简写”集合）
3. 单个 Route 对象：`{ name: xxx, paths: [...] }`

### 1. 完整结构示例（节选）
```yaml
upstreams:
  - name: user-service-upstream
    targets:
      - target: user-svc-1:8080
        weight: 100

services:
  - name: user-service
    upstream: user-service-upstream
    protocol: http
    port: 8080
    path: /api
    retries: 5

routes:
  - name: user-list
    service: user-service
    hosts: ["api.example.com"]
    paths: ["/v1/users"]
    methods: ["GET"]
    path_handling: v1
    strip_path: true
```

### 2. 路由简写（自动生成 service/upstream）
```yaml
- name: demo-route
  paths: ["/demo"]
  methods: ["GET", "POST"]
  path_handling: v1
  backend:
    protocol: http
    port: 8080
    path: /api
    targets:
      - target: demo-svc-1:8080
        weight: 100
      - target: demo-svc-2:8080
        weight: 100
```
自动派生：`demo-route-service` + `demo-route-upstream`。

### 3. 最简 Route（引用已存在 Service）
```yaml
routes:
  - name: echo-root
    service: echo
    paths: ["/"]
    methods: ["GET"]
    path_handling: v1
    strip_path: false
```

---

## 🔍 Dry-Run 与 Diff
```bash
kongctl apply -f examples/route-simple.yaml --dry-run --diff
```
输出包含：
- 分层树形视图：Route -> (Service -> Upstream -> Targets)
- 每个资源动作：创建 ✨ / 更新 ♻️ / 无变化
- 字段差异：`host: old -> new`、集合差异以 `+/-` 颜色标注
- 汇总统计：各类型创建 / 更新 / 无变化数量

可选增强：
| Flag | 作用 |
|------|------|
| `--compact` | 隐藏无变化项（none） |
| `--ascii` | 仅使用 ASCII（兼容纯文本终端） |
| `--no-color` | 关闭颜色（适合重定向到文件） |

---

## 🔐 安全与生产建议
- 永远不要将真实 Token 写入仓库；使用 `kongctl init` 或环境变量。
- `--tls-skip-verify` 仅限测试/内网使用。
- 在 CI 中使用时，推荐始终先执行一次 `--dry-run --diff` 并人工审阅。
- 大规模覆盖更新需显式加 `--overwrite`，避免意外修改稳定资源。

---

## 🛠️ 开发 & 贡献
```bash
make build   # 编译
make test    # 运行测试
make tidy    # 整理依赖
```
提交规范（Commit message）：`type(scope): 摘要` 例如：`feat(cli): 新增 apply 简写支持`。

欢迎提交：
- 新增更多资源支持（Plugin / Consumer / Certificate ...）
- 增强 diff 展示（JSON Patch / 富格式）
- 导出 / 反向生成配置（初始迁移场景）

---

## 🧭 Roadmap（方向性）
- [ ] Route / Service 插件级同步（基于名称或 inline 定义）
- [ ] OpenAPI -> Kong 资源映射入口
- [ ] `export`：从现有 Kong 集群生成可重放的 YAML
- [ ] Workspace 支持增强（隔离 diff）
- [ ] 失败重试与速率限制包装

> 也可在 Issue 中提出你的使用场景与痛点。

---

## 🤝 设计理念速记
| 关键词 | 说明 |
|--------|------|
| 最小侵入 | 默认不改动已存在资源（除非显式 `--overwrite`） |
| 幂等 | 同一配置多次 apply 结果一致 |
| 可观测 | Dry-run + diff 在执行前给出清晰预期 |
| 语法友好 | 支持多种 YAML 入口形式，降低上手成本 |
| 易扩展 | 客户端层保持薄封装，便于继续添加资源 |

---

## 📄 许可证
本项目使用 MIT License，详见 [LICENSE](./LICENSE)。

---

## 🙋 FAQ（摘选）
**Q: 为什么默认不更新现有 Service / Route？**  
A: 避免覆盖线下或手工紧急修改；通过 `--overwrite` 显式确认意图。

**Q: diff 为什么有的字段没显示？**  
A: 未在文件中声明的可选字段，不会触发比较；补齐它即可获得差异输出。

**Q: 目标权重 (weight) 改了却没生效？**  
A: 需加 `--overwrite`；默认仅补齐缺失 Target。

---

## 🧪 示例快速体验
```bash
# 1. 初始化
kongctl init --admin-url http://localhost:8001

# 2. 预览多路由简写计划（不实际变更）
kongctl apply -f examples/route-simple.yaml --dry-run --diff

# 3. 真正执行（仅创建缺失）
kongctl apply -f examples/route-simple.yaml

# 4. 覆盖修改（如调端口 / 路径）
kongctl apply -f examples/route-simple.yaml --overwrite
```

---

## ⭐ Star & 反馈
如果这个工具对你有帮助，欢迎点个 ⭐ Star；也欢迎提交 Issue / PR 让它更好用。

祝使用愉快！🚀
