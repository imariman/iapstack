<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack 地鼠吉祥物" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>开源、自托管的应用内购买验证与权益管理。</strong>
  <br>
  为 Apple App Store、Google Play 和 Huawei AppGallery 提供由后端掌控的统一模型。
  <br>
  使用 Go 构建，不绑定任何商店，可部署在任何地方。
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/zh-cn/"><strong>网站</strong></a>
  ·
  <a href="#quick-start"><strong>快速开始</strong></a>
  ·
  <a href="#deploy"><strong>部署</strong></a>
  ·
  <a href="#documentation"><strong>文档</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>参与贡献</strong></a>
</p>

<p align="center">
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
</p>

<p align="center">
  <a href="https://github.com/imariman/iapstack/actions/workflows/ci.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 License"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8.svg" alt="Go 1.26">
  <img src="https://img.shields.io/badge/status-prerelease-orange.svg" alt="Prerelease">
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack 运维仪表盘：演示项目的购买链路健康状况、悬停在某一天上的验证活动、已确认的商店收入以及沙盒测试金额" width="100%">
  </picture>
</p>

<p align="center">
  <sub>运维仪表盘，跟随系统主题显示（产品中可选择跟随系统 / 浅色 / 深色）。图中为合成演示数据。</sub>
</p>

> [!NOTE]
> 本文是[英文 README](README.md) 的译文。如两者内容不一致，以英文版为准。开发、仓库结构以及分步部署的详细说明仅以英文维护。

> [!IMPORTANT]
> IAPStack 是预发布软件。现有的 `v0.1.0-rc1` 和 `v0.1.0-rc2` 版本是早于当前发布门槛和安全修复的旧版预览，不支持用于生产环境。默认分支仍在开发中，其覆盖三个提供方的真实商店生命周期门槛尚未完成。

IAPStack 是一个自托管的后端和控制面板，用于验证应用内购买，并将商店交易转换为持久的应用权益。它为团队提供一个跨移动应用商店、由后端掌控的统一模型，同时让凭据、购买数据和访问决策保留在团队自己掌控的基础设施中。

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| 一次验证，处处适用 | 关注权益，而非收据 | 由你运行 |
| --- | --- | --- |
| 在统一的、与商店无关的模型背后，对 Apple App Store、Google Play 和 Huawei AppGallery 进行服务端验证。 | 应用直接查询客户可以访问什么，而无需解读原始收据和商店状态。 | 默认自托管：基础设施、凭据、购买数据和访问决策都归你所有。 |

<a id="current-scope"></a>

## 当前范围

- 对 Apple App Store、Google Play 和 Huawei AppGallery 的订阅及非消耗型终身购买进行服务端验证
- 基于 PostgreSQL、与商店无关的交易与权益模型
- 持久化的通知接收、生命周期对账以及已签名的出站 Webhook
- 需要认证的运维仪表盘（跟随系统 / 浅色 / 深色主题、可折叠的项目侧栏、可查看详情的活动与订阅图表），以及带版本的 HTTP/OpenAPI 契约
- 面向当前全部三个提供方的提供方无关 Flutter 包和各商店专用 Flutter 包
- 基于 Docker 和有文档说明的云平台，支持一体化或拆分式 API/Worker 部署

消耗型商品发放、客户迁移与别名合并、托管运营，以及上述三个之外的商店，均不在 v0.1 范围内。完整边界请参阅 [v0.1 范围](docs/v0.1-scope.md)。

<a id="sdk-roadmap"></a>

## SDK 路线图

Flutter 仍是 v0.1 发布范围内唯一的移动客户端：包括一个提供方无关的 HTTP 客户端，以及 Apple、Google Play 和 Huawei 配套包。其他 SDK 代码已按照 [#85](https://github.com/imariman/iapstack/issues/85) 中的扩展计划存在于仓库中。这些代码的存在并不会扩大 v0.1 的发布门槛，也不意味着这些包已成为生产版本。

| SDK | 已在仓库中实现 | 剩余工作 |
| --- | --- | --- |
| [Go 可信主机](sdk/go/README.md) | 客户会话、服务端权益查询和已签名 Webhook 处理 | 首次发布带版本的模块（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [TypeScript 可信主机](sdk/typescript/README.md) | 客户会话、权益查询、已签名 Webhook，以及可安装的 ESM/类型归档 | 首次发布到 npm（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [iOS (Swift)](sdk/ios/README.md) | HTTP 客户端、StoreKit 2 配套包、带 StoreKit 配置的可运行示例、根目录 SwiftPM 清单 | 首个 SDK 版本标签（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [Android (Kotlin)](sdk/android/README.md) | HTTP 客户端、Play Billing/HMS 适配器、可信主机示例，以及已暂存的 Maven 二进制/源码 jar | 首次发布到 GitHub Maven（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [React Native](sdk/react-native/README.md) | HTTP 客户端、原生 StoreKit/Play/Huawei 桥接、原生示例应用和打包好的配套包源码 | 首次发布到 npm（[#141](https://github.com/imariman/iapstack/issues/141)） |

[SDK 发布指南](docs/sdk-releases.md)介绍了安装、独立的预发布版本、包检查以及注册表配置。仓库中存在代码，并不代表注册表中已有对应的发布版本。

长期有效的应用 Bearer 令牌应保存在可信主机的后端，绝不能打包进移动应用二进制文件或浏览器 bundle 中。移动客户端使用短期有效的客户会话。

<a id="quick-start"></a>

## 快速开始

你需要安装带 Compose 的 Docker 以及 OpenSSL。在本仓库的克隆目录中，生成仅限本地使用的密钥并启动完整的技术栈：

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

请妥善保存生成的值，并在使用此 Compose 卷时继续复用它们。重新生成这些值并不会轮换现有的数据库密码或保护密钥。

然后打开 `http://127.0.0.1:8080/dashboard/`，使用引导管理员密钥连接。创建并保存一个长期有效的管理员密钥，从环境中移除 `IAPSTACK_BOOTSTRAP_ADMIN_KEY`，然后重新创建 API 容器。[运维指南](docs/operations.md)介绍了引导、密钥轮换、关停、备份、升级以及生产环境安全基线。

<a id="deploy"></a>

## 部署

在创建提供商资源之前，请先在[部署目标对比表](deploy/README.md)中比较账号要求、免费套餐限制和完整技术栈的成本。

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| 目标 | 形态 | 指南 |
| --- | --- | --- |
| Render | 一个运行 API 和 Worker 的免费 Web 服务、一个用于测试的免费 PostgreSQL 数据库，以及付费的一体化或拆分式 Blueprint | [Render 指南](deploy/render/README.md) |
| Heroku | API、Worker 和托管 PostgreSQL 运行在两个常驻的 Basic dyno 上；无免费运行环境 | [Heroku 指南](deploy/heroku/README.md) |
| DigitalOcean | 带 Worker 和迁移任务的 App Platform，或成本更低的 Droplet | [App Platform 指南](deploy/digitalocean/README.md) · [Droplet 指南](deploy/digitalocean/droplet.md) |
| Fly.io | 使用私有 Managed Postgres 的进程组；仅提供短期免费试用 | [Fly.io 指南](deploy/fly/README.md) |
| Koyeb | 通过官方 CLI 部署托管数据库、HTTPS API 和私有 Worker | [Koyeb 指南](deploy/koyeb/README.md) |
| Railway | PostgreSQL 加一个运行 API 和 Worker 的服务；Hobby 套餐约每月 USD 5 | [Railway 指南](deploy/railway/README.md) |
| Coolify | 在你自己的服务器上，通过仓库维护的 Docker Compose 定义部署完整技术栈 | [Coolify 指南](deploy/coolify/README.md) |
| 单台 VM | 成本最低的持续运行方案：在 Cloudflare Tunnel 之后的一台 Linux 机器上运行 PostgreSQL、迁移、API 和 Worker | [单 VM 指南](deploy/vps/README.md) |

### 最低成本方案：单台 VM

一台 Linux 机器通过 [`deploy/vps/compose.yaml`](deploy/vps/compose.yaml) 运行全部组件：私有 Docker 网络中的 PostgreSQL、一次性迁移容器、一个同时包含 API 和持久化 Worker 的一体化进程，以及一个 Cloudflare Tunnel 连接器，因此 VM 无需开放任何入站端口。Worker 需要一个持续运行的进程，因此小型常驻 VM 比可缩容至零的平台更合适。

| 主机 | 典型成本 | 适用情况 |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | 最多 4 个 Arm OCPU 和 24 GB 内存；一键 Resource Manager 栈。 |
| Google Cloud e2-micro（Free Tier，美国区域） | USD 0–4/月 | 1 GB 内存，适合低流量；提供 Cloud Shell 引导式教程。 |
| Hetzner Cloud CAX11 / CX22 | 约 EUR 4–5/月 | 计费可预测；通过一个 cloud-init 文件安装。 |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | 每月 USD 6 / 12 | 包含公网 IPv4；与 Hetzner 使用同一个 cloud-init 文件。 |
| 你已在运维的服务器 | USD 0 | 可在 NAT 之后运行，但接收商店通知需要持续在线。 |

你还需要一个 DNS 托管在 Cloudflare 免费套餐上的域名。创建资源前，请确认各提供商当前的条款。安装步骤、备份和升级见[英文 README 的 Deploy 部分](README.md#deploy)和[单 VM 指南](deploy/vps/README.md)。

<a id="design-principles"></a>

## 设计原则

**默认自托管。** 基础设施、凭据、购买数据和访问决策都归你所有。

**商店即适配器。** 各商店专用的 API 被隔离在共享契约之后；核心领域不依赖任何单一的应用市场。

**权益是产品边界。** 应用直接查询客户可以访问什么，而无需解读原始收据或商店状态。

**随处部署。** 目标是简单直接的容器部署，而不是依赖某个特定的云平台。

**独立实现。** 行为和安全要求以官方商店文档为准。为了互操作性和边界情况，可以研究其他开源项目，但 IAPStack 的代码是独立实现的。

<a id="documentation"></a>

## 文档

文档以英文编写。

- [v0.1 范围与架构](docs/v0.1-scope.md)
- [架构决策记录（ADR）](docs/adr/README.md)
- [HTTP API v1 与提供方通知契约](docs/api-v1.md)
- [机器可读的 OpenAPI v1 契约](contracts/openapi/v1.yaml)
- [运维与仪表盘指南](docs/operations.md)
- [v0.1 威胁模型与安全发布检查清单](docs/security.md)
- [安全策略与漏洞报告](SECURITY.md)
- [贡献指南](CONTRIBUTING.md)
- [支持指南](SUPPORT.md)
- [提供方无关的 Flutter SDK](sdk/flutter/iapstack/README.md)
- [原生 iOS Swift SDK 与 StoreKit 配套包](sdk/ios/README.md)
- [Go 可信主机 SDK](sdk/go/README.md)
- [TypeScript 可信主机 SDK](sdk/typescript/README.md)

<a id="development"></a>

## 开发

从源码构建、配置、测试以及仓库结构，见[英文 README 的 Development 部分](README.md#development)。提交 Pull Request 之前，请先阅读[贡献指南](CONTRIBUTING.md)。

<a id="roadmap"></a>

## 路线图

- [x] 定义核心领域、公开 API 契约和持久化模型
- [x] 实现初始的 Apple App Store、Google Play 和 Huawei AppGallery 适配器
- [x] 添加 PostgreSQL 迁移和面向生产形态的 Docker 配置
- [x] 构建提供方无关的 Flutter SDK、三个提供方专用的 Flutter SDK 以及仪表盘
- [x] 支持生命周期对账和已签名的出站 Webhook
- [x] 发布机器可读的 v1 API 契约，并提供客户端兼容性检查
- [x] 实现 Go、TypeScript、Swift、Kotlin 和 React Native SDK
- [x] 支持客户迁移、别名合并、RevenueCat 导入和财务报表导入
- [x] 添加 Kubernetes 部署清单
- [ ] 完成 Apple App Store、Google Play 和 Huawei 的生命周期发布门槛，并发布 `v0.1.0-rc.1`
- [ ] 处理候选版本反馈，并发布稳定版 `v0.1.0`
- [ ] 发布带版本的主机、Android、iOS 和 React Native 包（[#141](https://github.com/imariman/iapstack/issues/141)）
- [ ] 添加 Amazon Appstore 及更多商店适配器

已完成的项目表示仓库中已有实现，并不代表已达到生产发布就绪状态。

<a id="license"></a>

## 许可证

基于 [Apache License 2.0](LICENSE) 授权。

参与本项目即表示你同意遵守[行为准则](CODE_OF_CONDUCT.md)。关于贡献和获取支持的途径，请参阅 [CONTRIBUTING.md](CONTRIBUTING.md) 和 [SUPPORT.md](SUPPORT.md)。
