<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack 地鼠吉祥物" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>自托管的应用内购买验证与权益管理。</strong>
  <br>
  为 Apple App Store、Google Play 和 Huawei AppGallery 提供由后端掌控的统一模型。
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/zh-cn/">网站</a>
  ·
  <a href="#quick-start">快速开始</a>
  ·
  <a href="#deploy">部署</a>
  ·
  <a href="#documentation">文档</a>
  ·
  <a href="contracts/openapi/v1.yaml">OpenAPI</a>
</p>

<p align="center">
  <a href="https://github.com/imariman/iapstack/actions/workflows/ci.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="https://github.com/imariman/iapstack/actions/workflows/codeql.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/codeql.yml/badge.svg" alt="CodeQL status"></a>
  <a href="https://github.com/imariman/iapstack/releases"><img src="https://img.shields.io/github/v/release/imariman/iapstack?include_prereleases&amp;sort=semver&amp;color=orange" alt="Latest prerelease"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 License"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/imariman/iapstack" alt="Go version from go.mod"></a>
</p>

<p align="center">
  <sub>
    <a href="README.md">English</a>
    · <a href="README.de.md">Deutsch</a>
    · <a href="README.es.md">Español</a>
    · <a href="README.fr.md">Français</a>
    · <a href="README.it.md">Italiano</a>
    · <a href="README.pt-BR.md">Português (Brasil)</a>
    · <a href="README.tr.md">Türkçe</a>
    · <a href="README.ru.md">Русский</a>
    · <a href="README.ja.md">日本語</a>
    · <a href="README.ko.md">한국어</a>
    · <strong>简体中文</strong>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack 运维仪表盘：演示项目的购买链路健康状况、悬停在某一天上的验证活动、已确认的商店收入以及沙盒测试金额" width="100%">
  </picture>
</p>

> [!NOTE]
> 本文是[英文 README](README.md) 的译文。如两者内容不一致，以英文版为准。

IAPStack 是一个后端和控制面板，在服务端验证应用内购买，并将商店交易转换为持久的权益。你的应用只需提出一个问题：*这位客户可以访问什么？* 无论购买来自哪个商店，答案都是一致的。凭据、购买数据和访问决策都保留在你自己运行的基础设施中。

| 一次验证，处处适用 | 关注权益，而非收据 | 由你运行 |
| --- | --- | --- |
| 在统一的、与商店无关的模型背后，对三个商店进行服务端验证。商店 API 被隔离在适配器中。 | 应用读取访问状态，而无需解读原始收据和各商店特有的状态。 | 一个 Go 二进制文件加 PostgreSQL。可通过 Docker 运行在任意云平台或你现有的 VM 上。 |

> [!IMPORTANT]
> IAPStack 是预发布软件，尚不支持用于生产环境。`v0.1.0-rc1` 和 `v0.1.0-rc2` 版本是旧版预览；首个受支持的候选版本将在三个提供方全部通过真实商店生命周期门槛后发布。

<a id="features"></a>

## 功能

- 对 Apple App Store、Google Play 和 Huawei AppGallery 的订阅及非消耗型终身购买进行**验证**
- 通过持久化的商店通知接收和定时对账实现**生命周期跟踪**
- **已签名的出站 Webhook**，让你的后端获知每一次权益变更
- **运维仪表盘**，用于管理应用、客户、队列和 Webhook 投递，支持浅色和深色主题
- **带版本的 HTTP API**，附带机器可读的 [OpenAPI 契约](contracts/openapi/v1.yaml)
- 面向 Flutter、iOS、Android 和 React Native 的**客户端 SDK**，以及供你自己的后端使用的 Go 和 TypeScript SDK
- **灵活的拓扑**：一个一体化进程，或拆分为独立的 API 和 Worker 服务

消耗型商品发放、托管运营，以及上述三个之外的商店，均不在 v0.1 范围内。客户迁移、RevenueCat 导入、产品方案（offerings）和 Apple 财务报表已在 `main` 上提供，并可在 [`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1) 中预览，但它们不属于 v0.1 发布门槛。完整边界请参阅 [v0.1 范围](docs/v0.1-scope.md)。

<a id="quick-start"></a>

## 快速开始

你需要安装带 Compose 的 Docker 以及 OpenSSL。在本仓库的克隆目录中，生成本地密钥并启动技术栈：

```sh
export IAPSTACK_POSTGRES_PASSWORD="$(openssl rand -base64 32)"
export IAPSTACK_PROTECTION_ACTIVE_KEY_ID="local-1"
export IAPSTACK_PROTECTION_KEYS="{\"local-1\":\"$(openssl rand -base64 32)\"}"
export IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32)"
export IAPSTACK_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base64 48)"
export IAPSTACK_METRICS_BEARER_TOKEN="$(openssl rand -base64 48)"
docker compose -f deploy/compose.yaml up --build -d --wait
curl --fail http://127.0.0.1:8080/readyz
```

请妥善保存这些值：Compose 卷依赖于它们，重新生成这些值并不会轮换现有的数据库密码或保护密钥。

打开 `http://127.0.0.1:8080/dashboard/`，使用引导管理员密钥登录。创建一个长期有效的管理员密钥，然后移除 `IAPSTACK_BOOTSTRAP_ADMIN_KEY` 并重新创建 API 容器。其余内容见[运维指南](docs/operations.md)：密钥轮换、备份、升级以及生产环境安全基线。

<a id="deploy"></a>

## 部署

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

每个部署目标都运行同一个容器镜像，包括 PostgreSQL、迁移步骤、API，以及一个必须持续运行的 Worker。在创建资源之前，请先在[部署目标对比表](deploy/README.md)中比较账号要求、免费套餐限制和完整技术栈的成本。

| 目标 | 形态 | 指南 |
| --- | --- | --- |
| **单台 VM**（Oracle、Google Cloud、Hetzner、DigitalOcean 或你自己的服务器） | 成本最低的持续运行方案：在 Cloudflare Tunnel 之后的一台 Linux 机器上运行完整技术栈，无需开放任何入站端口。使用 Oracle Cloud Always Free 时每月 USD 0 起。 | [单 VM 指南](deploy/vps/README.md) |
| Render | 用于评估的免费一体化服务和一次性 PostgreSQL；付费的一体化或拆分式 Blueprint | [Render](deploy/render/README.md) |
| Railway | PostgreSQL 加一个 API/Worker 服务；通常在 USD 5 的 Hobby 套餐内 | [Railway](deploy/railway/README.md) |
| Heroku | API、Worker 和托管 PostgreSQL 运行在两个 Basic dyno 上；无免费套餐 | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | 通过 App Spec 部署 API、Worker 和迁移任务 | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | 使用私有 Managed Postgres 的进程组 | [Fly.io](deploy/fly/README.md) |
| Koyeb | 通过 CLI 部署托管数据库、HTTPS API 和私有 Worker | [Koyeb](deploy/koyeb/README.md) |
| Coolify | 在你自己的服务器上，通过仓库中的 Compose 定义部署完整技术栈 | [Coolify](deploy/coolify/README.md) |
| Kubernetes | 用于 API、Worker 和迁移任务的普通清单；PostgreSQL 需自行提供 | [Kubernetes](deploy/k8s/README.md) |

可缩容至零的平台和无服务器平台并不适合：Worker 需要接收商店通知、投递 Webhook，并每分钟执行一次对账。

<a id="sdks"></a>

## SDK

移动应用通过短期有效的客户会话与 IAPStack 通信。长期有效的应用密钥应保存在你的后端，绝不能打包进移动应用二进制文件或浏览器 bundle 中。

| 平台 | 包 | 状态 |
| --- | --- | --- |
| Flutter | [提供方无关的客户端](sdk/flutter/iapstack/README.md)，以及 [Apple](sdk/flutter/iapstack_apple/README.md)、[Google Play](sdk/flutter/iapstack_google_play/README.md) 和 [Huawei](sdk/flutter/iapstack_huawei/README.md) 配套包 | v0.1 发布范围 |
| iOS (Swift) | [HTTP 客户端和 StoreKit 2 配套包](sdk/ios/README.md) | 已实现，尚未发布 |
| Android (Kotlin) | [带 Play Billing 和 HMS 适配器的 HTTP 客户端](sdk/android/README.md) | 已实现，尚未发布 |
| React Native | [HTTP 客户端和原生 StoreKit/Play/Huawei 桥接](sdk/react-native/README.md) | 已实现，尚未发布 |
| Go（后端） | [客户会话、权益查询、Webhook 验证](sdk/go/README.md) | 已实现，尚未发布 |
| TypeScript（后端） | [客户会话、权益查询、Webhook 验证](sdk/typescript/README.md) | 已实现，尚未发布 |

注册表发布的进展在 [#141](https://github.com/imariman/iapstack/issues/141) 中跟踪。[SDK 发布指南](docs/sdk-releases.md)介绍了从源码安装、版本管理和发布自动化。

<a id="documentation"></a>

## 文档

文档以英文编写。

| 指南 | 涵盖内容 |
| --- | --- |
| [运维](docs/operations.md) | 密钥、配置、仪表盘、商店通知设置、备份、升级、发布 |
| [HTTP API v1](docs/api-v1.md) | 端点、提供方凭据、通知、Webhook 验证 |
| [v0.1 范围与架构](docs/v0.1-scope.md) | 系统上下文、核心不变量、提供方切片 |
| [安全](docs/security.md) | 威胁模型与发布安全检查清单 |
| [架构决策](docs/adr/README.md) | 主要边界为何存在 |
| [部署目标](deploy/README.md) | 提供商对比与各提供商指南 |
| [发布手册](docs/releases/v0.1.0-rc.1.md) | 三个提供方的真实商店发布门槛 |

<a id="development"></a>

## 开发

请使用 `go.mod` 中声明的 Go 工具链。启动 PostgreSQL、应用迁移，并在一个进程中运行 API 和 Worker：

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

服务器还需要[快速开始](#quick-start)中的保护密钥；所有设置都列在[配置参考](docs/operations.md#configuration-reference)中。API 监听 `:8080`，提供 `/healthz` 和 `/readyz`，并在 `/dashboard/` 内嵌仪表盘。

运行服务端检查和端到端发布门槛：

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

[贡献指南](CONTRIBUTING.md)包含 SDK 检查、集成测试设置、架构概览和代码约定。

<a id="repository-layout"></a>

### 仓库结构

```text
.
├── cmd/iapstack/       # API、Worker 和迁移入口
├── internal/
│   ├── core/           # 客户、交易、商品和权益
│   ├── verification/   # 提供方无关的购买验证
│   ├── stores/         # Apple、Google Play 和 Huawei 适配器
│   ├── credentials/    # 受保护的应用凭据
│   ├── protection/     # 敏感数据保护端口
│   ├── persistence/    # PostgreSQL 仓储
│   └── platform/       # 配置、日志和密钥环实现
├── dashboard/          # 内嵌的运维仪表盘
├── contracts/          # OpenAPI 和 Webhook 契约
├── sdk/                # Flutter、iOS、Android、React Native、Go 和 TypeScript SDK
├── deploy/             # Compose、云平台、VM 和 Kubernetes 定义
└── docs/               # 指南、ADR 和发布手册
```

<a id="roadmap"></a>

## 路线图

- [x] 与商店无关的领域模型、PostgreSQL 持久化和带版本的 OpenAPI 契约
- [x] Apple App Store、Google Play 和 Huawei AppGallery 适配器
- [x] 生命周期对账和已签名的出站 Webhook
- [x] 运维仪表盘，以及面向云平台、VM 和 Kubernetes 的部署定义
- [x] Flutter、Swift、Kotlin、React Native、Go 和 TypeScript SDK
- [x] 客户迁移、RevenueCat 导入、产品方案和 Apple 财务报表（`v0.2.0-alpha.1`）
- [x] 订阅指标、客户支持工具和 Apple 退款消耗信息响应（已在 `main` 中，尚未发布）
- [ ] 通过三个提供方的真实商店门槛并发布 `v0.1.0-rc.1`
- [ ] 发布稳定版 `v0.1.0`
- [ ] 发布带版本的 SDK 包（[#141](https://github.com/imariman/iapstack/issues/141)）
- [ ] MCP 端点，让 AI 智能体以最小权限密钥查询和操作 IAPStack（[#285](https://github.com/imariman/iapstack/issues/285)）
- [ ] Google Play 和 Huawei 财务报表收入确认
- [ ] Amazon Appstore 及更多商店适配器

已勾选的项目表示仓库中已有实现，并不代表已达到生产就绪状态。

<a id="contributing"></a>

## 参与贡献

欢迎提交缺陷报告、修复以及分享商店相关的专业知识。提交 Pull Request 之前，请先阅读[贡献指南](CONTRIBUTING.md)；如有疑问，请参阅[支持指南](SUPPORT.md)；如需报告漏洞，请参阅[安全策略](SECURITY.md)。参与本项目须遵守[行为准则](CODE_OF_CONDUCT.md)。

<a id="license"></a>

## 许可证

[Apache License 2.0](LICENSE)
