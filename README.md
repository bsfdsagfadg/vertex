# Vertex AI Proxy

免费使用 Google Gemini 模型的代理工具。将 **OpenAI 兼容的 API 请求**无缝转换为对 Google 匿名端点的调用——让你的客户端以为在调用 OpenAI，实际上使用的是免费的 Gemini 服务。也支持原始Gemini格式

**免安装、解压即用的绿色软件。** 全面支持 Windows、Linux、macOS 以及 Android 手机等平台。

## ✨ 核心特性

- **完整兼容 OpenAI 接口**：支持聊天（流式/非流式）、工具调用（Function Calling）、多模态输入（图片/文件）。
- **丰富的多媒体支持**：支持文生图、图片编辑、语音合成（TTS）。
- **内置反爬突破**：内置 TLS 指纹伪装及 reCAPTCHA token 自动获取，轻松通过 Google 匿名端点校验。
- **内置代理节点池**：内嵌 sing-box 内核，支持批量导入订阅和节点，提供并发竞速功能，有效应对429
- **可视化管理面板**：提供精美的 Web 后台，无需修改 JSON 文件，在浏览器中即可轻松管理 API 密钥、模型别名、代理节点和系统设置。
- **高级功能**：支持 Token 计数、Gemini 原生端点透传、假非流输出等。

## 🚀 三步上手

**1. 下载解压**：下载对应平台的压缩包并解压到任意位置。

**2. 一键启动**：
- **Windows**：双击运行 `启动.bat`
- **Linux/macOS**：终端执行 `sh start.sh`
- **Android (Termux)**：终端执行 `sh start.sh`

**3. 配置密钥**：
首次启动时控制台会输出**管理员密码**。使用浏览器访问 `http://127.0.0.1:2156/admin/` 登录管理面板。
进入左侧「密钥」菜单，添加一个自定义的 API Key（如 `sk-mykey123`），必须以 `sk-` 开头，或点击“✨”按钮随机生成。

> **如何使用？**
> 在你的客户端（如 Cherry Studio、ChatBox 等）中，将 API Key 填为刚才设置的 `sk-...`，API 地址填为 `http://127.0.0.1:2156/v1` 即可开始使用！

**完整的分平台部署教程**（包括开机自启、代理配置、手机部署、常见问题解答）见 **[部署指南](部署指南.md)**。

## 🛠 自己编译（可选）

如果你想从源码自行编译：

```bash
go build -tags with_utls -o vertex-proxy ./cmd/vproxy
go build -tags with_utls -o vertex-proxy.exe ./cmd/vproxy
```

交叉编译示例（例如在 macOS/Windows 上编译 Linux 适用版本）：
```bash
# 在 Linux/macOS 上编译 Linux AMD64 版本
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags with_utls -trimpath -ldflags="-s -w" -o vertex-proxy ./cmd/vproxy
# 在 Windows PowerShell 上编译 Linux ARM64 版本
& { $env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm64"; go build -tags with_utls -trimpath -ldflags="-s -w" -o vertex-proxy ./cmd/vproxy }
```

## ⚙️ 配置说明

强烈建议直接使用**管理面板**的「设置」页进行配置修改，所有修改即时生效，无需重启。
如果需要手动修改，配置文件路径为 `config/config.json`：

| 选项 | 默认值 | 说明 |
|------|------|------|
| `port_api` | 2156 | 服务监听端口 |
| `admin_password` | 自动生成 | 管理面板登录密码 |
| `max_retries` | 10 | 请求失败重试次数 |
| `proxy_url` | 空 | 出站代理地址 (如 `http://127.0.0.1:7890`) |
| `parallel_pool_enabled` | true | 是否开启并发竞速节点池 |

> **提示**：在模型名（如 `gemini-3.5-flash`）前加上 `假非流-` 前缀（如 `假非流-gemini-3.5-flash`），可将真实流式请求转为单个 SSE 数据包返回，适合需要流式接口但期望完整响应的场景。

详细配置说明请参阅 [部署指南](部署指南.md#配置怎么改)。

