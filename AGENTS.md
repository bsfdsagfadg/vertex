# AGENTS.md — Vertex AI Proxy 工程指南

本文件供 AI Agent 与开发者查阅：记录项目架构分层职责、开发校验指令、单文件规模与测试规范，以及系统关键架构铁律。

---

## 一、 架构领域与 Package 职责概览

项目划分为四个架构领域（接入适配域 `api`、核心引擎域 `engine`、节点领域域 `node`、基础设施域 `infra`），`internal/` 下包含 17 个独立 package 及 1 个静态资源目录。代码搜索与定位请使用 `glob` / `grep` 工具动态检索，禁止维护或依赖静态文件名清单。

### 1. 接入适配域 (`internal/api`)
- **`internal/api`**：Gemini 原生 REST 路由入口（`/v1beta/models/*` 等）、模型家族物理隔离 Pipeline 调度（文本、生图、语音）、后台管理 API 及认证/流控中间件。全量依赖经 `ServerDeps` 显式构造期注入。

### 2. 核心引擎域 (`internal/engine`)
- **`internal/engine/transform`**：统一模型解析 (`ModelResolver`)、文本与生图双规格能力白名单矩阵、模型家族自治策略与独占变量构建 (`BuildVariables`)、强类型 DTO 与 Schema 定义。
- **`internal/engine/vertex`**：上游 Vertex/Gemini 通信与协议处理、SSE 增量流式解析 (`Scanner`/`Transform`)、泛型竞速调度引擎 (`RaceEngine`)；全局统一错误内核（`errors.go` 的 `NormalizeError`）亦驻留于此。
- **`internal/engine/recaptcha`**：reCAPTCHA Token 池自动化管理与后台刷新调度。

### 3. 节点领域域 (`internal/node`)
- **`internal/node/exitpool`**：出口竞速节点池实例化管理（`Manager` + `Hooks`）、状态转换与健康度调度。
- **`internal/node/entrypool`**：前置代理节点池实例化管理与路由切换。
- **`internal/node/nodestore`**：出口与前置节点持久化的通用 SQLite 存储内核（提供纯函数+数据行视图抽象）。
- **`internal/node/importer`**：多格式前置/出口节点订阅与配置导入引擎（支持 URI、Clash、V2Ray、JSON 等格式解析），纯解析库（无节点池副作用）。

### 4. 基础设施域 (`internal/infra`)
- **`internal/infra/transport`**：网络代理、SOCKS5 环回与拨号器 (`Dialer`) 构建、节点 URI 解析缓存 (`IRCache`)；对节点域需求经消费方窄接口 `NodeNamer`/`EntrySource` 反转。
- **`internal/infra/spool`**：TLS 会话复用连接池与 Http2 多路复用传输。
- **`internal/infra/netx`**：跨平台底层网络适配与 Socket 优化。
- **`internal/infra/config`**：系统配置加载、环境变量解析与动态模型预设。
- **`internal/infra/db`**：SQLite 数据库连接初始化（`db.Open` 实例语义）与基础持久化支持。
- **`internal/infra/jsonx`**：高性能 JSON 序列化与反序列化工具库封装。
- **`internal/infra/logger`**：结构化日志输出与等级控制。
- **`internal/infra/cli`**：TUI 终端监控面板与交互命令行支持。
- **`internal/infra/admin`**：后台 Web 静态资源（HTML/CSS/JS）与模版文件嵌入 (`embed`) 管理。

> **静态资源目录**：`internal/assets` 存放通用静态资源与预设资产（非 Go package，被 `cmd/vproxy/main.go` 与打包脚本按相对路径引用）。
> **目录治理原则**：代码新增与重构需遵循 **package 内按功能语义切分** 原则（零破坏重构），不得随意增加冗余层级。

---

## 二、 校验指令与静态门禁

### 1. 常用开发校验命令
- 代码格式检查：`gofmt -l internal cmd scripts`
- 代码格式自动修复：`gofmt -w -s internal cmd scripts`
- 单包测试：`go test ./internal/<domain>/<pkg>/...`（如 `go test ./internal/engine/vertex/...`）
- 全量测试：`go test ./...`
- 静态检查：`go vet ./...`
- 静态分析（**告警必须归零**，U1000 / S1009 等一律清零后再提交）：`staticcheck ./...`
- 聚合静态检查（gopls 编辑器分析器的命令行等价物，覆盖 `writestring` / `unusedparams` / `simplifycompositelit` / `modernize`(b.Loop)，**告警必须归零**）：`go run ./scripts/gochecks`
- 全功能构建（Sing-Box / UTLS / QUIC 需要构建标签）：

  ```powershell
  go build -tags "with_utls with_quic" -o vertex-proxy.exe ./cmd/vproxy
  ```

- 单文件行数复查（非测试文件 >500 行）：

  ```powershell
  Get-ChildItem -Recurse -Include *.go -Exclude "node_modules",".git",".kilo","dist" | Where-Object { $_.FullName -notmatch '\\\.kilo\\' -and $_.FullName -notmatch '_test\.go$' } | Select-Object -Property FullName, @{Name="Lines";Expression={(Get-Content $_.FullName | Measure-Object -Line).Lines}} | Where-Object { $_.Lines -gt 500 }
  ```

### 2. 静态分析门禁约定（防回潮）

1. **提交门禁**：仓库启用 `.githooks/pre-commit` 钩子（`git config core.hooksPath .githooks`），提交前自动执行 `gofmt` + `go vet` + `staticcheck` + `gochecks` 四重门禁，告警非零即阻止提交。**任何人（含 Agent）提交前必须保证四者输出为空**。钩子为**纯 sh 实现，跨平台**（Windows Git Bash / Linux / macOS），依赖 `go` 与 `staticcheck`（`go install honnef.co/go/tools/cmd/staticcheck@latest`）在 PATH 中；`core.hooksPath` 为本地配置不入库，换环境克隆后需重新执行启用命令。
2. **新增/修改代码要求**：新增导出函数不得留下"仅测试引用"的死代码；删除代码时同步清理随之失效的 import（`go build` / `go test` 会兜底抓出）。新增参数必须被使用或命名为 `_`（`gochecks` 的 `unusedparams` 会拦截）；字符串拼接严禁用 `sb.WriteString(a + b)` 形式（`gochecks` 的 `writestring` 会拦截）；嵌套复合字面量省略可推断类型（`gofmt -s` / `gochecks` 的 `simplifycompositelit`）；基准测试循环使用 `b.Loop()`（`gochecks` 的 `modernize` 会拦截）。
3. **gochecks 工具说明**：`scripts/gochecks/` 为挂在根模块下的聚合检查器（无独立 go.mod，直接 `go run ./scripts/gochecks`），豁免规则对齐 gopls 行为（接口实现方法参数、方法接收者不报）。`infertypeargs` 检查器依赖完整类型推断、仅存在于 gopls 内部，无命令行等价物，依靠编辑器提示人工处理（无行为风险）。若需新增检查项，在 `scripts/gochecks/main.go` 注册进 `checkers` map，同步更新本文件与 `.githooks/pre-commit` 注释。
4. **导出死代码巡检（staticcheck 盲区）**：staticcheck 的 U1000 不检查导出符号，对导出函数的清理需人工复核或定期分析调用链：
   - 生产与测试均零引用的导出符号 → 人工复核后删除；
   - 测试专用 API（如 `config.StaticProvider`、`vertex` 包 `testhooks.go` 内的测试钩子）→ 必须保留；
   - 定义在 `_test.go` 内的未使用函数需结合 `staticcheck` U1000 结果交叉确认。

---

## 三、 单文件规模分级与测试规范

### 1. 单文件行数审查梯队

| 规模区间 | 判定 |
| :--- | :--- |
| < 300 行 | 绿色健康区（理想状态） |
| 300 ~ 400 行 | 正常核心业务区 |
| 400 ~ 500 行 | 预警审视区（检查多重职责，能拆则拆） |
| > 500 行 | 特许例外区（纯数据映射表、不可分割单一协议解析器） |

> **说明**：行数线为**预警指导指标**而非硬性阻断红线。代码治理核心原则是**单一职责原则（SRP）**与**高内聚低耦合**。严禁为了凑行数而盲目拆分逻辑紧密的代码块或削减有效测试用例。

### 2. 测试文件专项规范
1. **行数上限与弹性**：单个 `*_test.go` 文件原则上建议控制在 **600 行**以内。若包含大量表驱动测试数据（Table-Driven Test Fixtures）或 Mock 报文，且保持单一测试职责，可作为合理例外保留；对于混合了多子场景的超长测试，需按子场景拆分。
2. **旁路测试原则（Colocated Testing）**：测试代码必须与被测业务逻辑处于同一 package，并采用 1 对 1 文件命名对齐（如 `auth.go` 对应 `auth_test.go`）。**严禁创建 `refactor_test.go`、`temp_test.go` 或 `fix_test.go` 等临时性/大一统测试文件**。重构或临时修复新增的测试用例必须在验证后及时归位至对应模块的标准测试文件中。
3. **测试执行性能**：单元测试单用例耗时原则上控制在 **500ms** 以内。**严禁硬编码大额休眠（如 `time.Sleep(10 * time.Second)`）**，模拟网络超时或挂起须使用 `context.WithTimeout` 或 Mock 时间控制。

---

## 四、 关键架构铁律与约定

1. **纯 Gemini 原生 REST 契约铁律 (Gemini Native Only)**：全链路采用 Gemini 原生 REST 规范（端点 `/v1beta/models/*`、`/v1beta1/models/*` 等），彻底剥离 OpenAI 等中转兼容层，所有入站及出站交互严格对齐 Gemini 强类型 `GeminiRequest` / `GeminiResponse` / `GeminiChunk` 数据契约。
2. **唯一模型解析口铁律 (Single Model Resolver)**：所有入站请求的模型名称解析必须统一经由 `ModelResolver.ResolveModel` 处理，统一完成 GCP 规范前缀剥离、假非流修饰剥离、别名归一与家族绑定（`FamilyText` / `FamilyImage` / `FamilyAudio`），入口处消除任何硬编码字符串判断。
3. **物理隔离 Pipeline 铁律 (Dedicated Family Pipelines)**：不同模型家族在 API 调度层彻底物理隔离（文本真流式/非流式、生图及单包聚合流式守护、语音 TTS 纯二进制交付），严禁跨家族逻辑交织与单体核心文件复辟。
4. **家族自治与双规格矩阵铁律 (Family Autonomy & Dual Spec Matrix)**：
   - **双模型规格白名单矩阵**：文本（`TextModelSpec`）与生图（`ImageModelSpec`）分别维护家族硬性能力边界；
   - **全生命周期策略**：通过各家族 `ModelStrategy` 独立实施 `Enhance`（默认参数与模态注入）、`Validate`（白名单矩阵硬拦截）、`Prepare`（出口前特化清洗）与 `BuildVariables`（家族独占 variables 构建）。
5. **家族独占变量正向构建铁律 (Dedicated BuildVariables)**：
   - **文本家族**：实施同 Role 连续消息合并、空 Part 过滤、历史思维链签名注入、TrailingModelFix 与系统指令降级；
   - **生图/语音家族**：采用正向白名单参数提取，硬性清空无关 Tools / ThinkingConfig，杜绝跨家族变量污染。
6. **强类型与零 map 往返铁律 (Strong Typing & Zero Map Roundtrip)**：核心领域业务层必须维持强类型 `struct` 传递（利用指针 + `omitempty` 杜绝脏数据污染），严禁退回 `map[string]any` 中转与 in-place 修改范式。
7. **竞速引擎零模型感知铁律 (Model-Agnostic Race Engine)**：竞速引擎泛型化 (`RunRace[T]`)，恒满窗口补位模型（失败/非胜出收集即瞬时换点补位，发射预算 = `(max_retries+1)×并发数` 封顶，每候选独立超时），零模型业务感知；响应有效性与动态超时判定退回下行阶段由各家族策略 (`CalculateIdleTimeouts`、`IsValidChunk` / `IsValidResponse`) 自治实施。
8. **统一错误内核与安全拦截放行铁律 (Unified Error & Safety Pass-through)**：上游响应通过 `errors.go` 归一化分类 (`NormalizeError`)。安全拦截等极性拦截报文予以结构化放行，确保客户端能准确识别安全拒绝原因而不会误判为网络崩溃。
9. **显式装配与零包级状态铁律 (Explicit Assembly & Zero Package State)**：`cmd/vproxy/main.go` 为唯一组合根，全链路经构造函数显式注入装配（`db.Open` → `IRCache` → 出口/前置 Manager 实例（含 `Hooks`）→ `DialerDeps` → `VertexAIClient` → `ServerDeps`）。严禁包级函数指针变量、包级可变单例与全局注册表回归；跨域失效联动一律经 `Hooks` 结构体构造期注入。进程级单例仅限既定白名单类别（build 标签注入变量、TUI 状态、config 进程缓存、只读映射表等）。
10. **消费方窄接口铁律 (Consumer-Side Narrow Interfaces)**：跨域抽象一律由**消费方定义接口、提供方实现、main 构造期注入**：vertex 的 `NodePool`、recaptcha 的 `NodeSource`、transport 的 `NodeNamer` / `EntrySource`、api 的 `ImportParser` / `TokenVerifier`。严禁提供方反向定义大而全接口，或绕过接口直接 import 跨域具体类型。
11. **领域依赖方向规则 (Domain Dependency Direction)**：
    - **R1**：`infra` 域保持零内部依赖；对上层需求一律经消费方窄接口 + main 装配反转。唯一豁免：跨域窄接口签名中的**类型位只读结构体传递**（如 `transport.EntrySource.SelectableNodes() []entrypool.Node`），无行为依赖、不引入回调路径；
    - **R2**：`node` → `infra` 允许（向下依赖，无环）；
    - **R3**：`engine` → `infra` + `node` 允许（经窄接口消费）；
    - **R4**：`api` → 全部域允许；
    - **R5**：任何"下层回调上层/跨域失效联动"需求，一律经接口/Hooks 构造期注入实现，严禁包级函数指针变量，编译期不得成环。
