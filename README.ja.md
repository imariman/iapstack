<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStackのGopherマスコット" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>オープンソースでセルフホスト型の、アプリ内課金の検証とエンタイトルメント管理。</strong>
  <br>
  Apple App Store、Google Play、Huawei AppGalleryを、バックエンドが管理する1つのモデルで扱います。
  <br>
  Goで構築。ストア非依存。どこにでもデプロイ可能。
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ja/"><strong>ウェブサイト</strong></a>
  ·
  <a href="#quick-start"><strong>クイックスタート</strong></a>
  ·
  <a href="#deploy"><strong>デプロイ</strong></a>
  ·
  <a href="#documentation"><strong>ドキュメント</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>コントリビューション</strong></a>
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
  · <strong>日本語</strong>
  · <a href="README.ko.md">한국어</a>
  · <a href="README.zh-CN.md">简体中文</a>
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
    <img src="assets/dashboard-light.png" alt="IAPStack運用ダッシュボード：デモプロジェクトの購入ルートの状態、ホバー中の1日を含む検証アクティビティ、計上されたストア収益、サンドボックスのテスト金額" width="100%">
  </picture>
</p>

<p align="center">
  <sub>システムテーマに追従する運用ダッシュボード（製品内ではシステム / ライト / ダーク）。合成デモデータを表示しています。</sub>
</p>

> [!NOTE]
> これは[英語版README](README.md)の翻訳です。両者に相違がある場合は英語版が正となります。開発、リポジトリ構成、手順ごとのデプロイの詳細は英語版でのみ管理しています。

> [!IMPORTANT]
> IAPStackはプレリリースソフトウェアです。既存の`v0.1.0-rc1`および`v0.1.0-rc2`
> リリースは、現在のリリースゲートとセキュリティ修正より前のレガシープレビューであり、
> 本番環境での使用はサポートしていません。デフォルトブランチは引き続き開発中で、
> 3プロバイダーの実ストアでのライフサイクルゲートはまだ完了していません。

IAPStackは、アプリ内課金を検証し、ストアのトランザクションを永続的なアプリケーションの
エンタイトルメントに変換する、セルフホスト型のバックエンド兼コントロールパネルです。
認証情報、購入データ、アクセスの判定を自分たちが管理するインフラに置いたまま、
モバイルストア全体をバックエンドが管理する1つのモデルで扱えます。

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| 一度の検証で、すべてのストアに | レシートではなくエンタイトルメント | 自分で運用できる |
| --- | --- | --- |
| Apple App Store、Google Play、Huawei AppGalleryのサーバーサイド検証を、ストアに依存しない1つのモデルの背後で行います。 | アプリは生のレシートやストアの状態を解釈する代わりに、顧客が何にアクセスできるかを問い合わせます。 | デフォルトでセルフホスト：インフラ、認証情報、購入データ、アクセスの判定はすべてあなたのものです。 |

<a id="current-scope"></a>

## 現在のスコープ

- Apple App Store、Google Play、Huawei AppGalleryのサブスクリプションと、
  非消耗型の買い切り購入のサーバーサイド検証
- PostgreSQLを基盤とする、ストアに依存しないトランザクションとエンタイトルメントのモデル
- 永続的な通知の取り込み、ライフサイクルの照合、署名付きの送信Webhook
- バージョン管理されたHTTP/OpenAPIコントラクトを備えた、認証付きの運用ダッシュボード
  （システム / ライト / ダークテーマ、折りたたみ可能なプロジェクトレール、
  詳細を確認できるアクティビティとサブスクリプションのチャート）
- 現在の3プロバイダーすべてに対応する、プロバイダー非依存およびストア別のFlutterパッケージ
- Dockerとドキュメント化されたクラウドターゲットによる、API/ワーカーのコンパクト構成または分割構成のデプロイ

消耗型アイテムの付与、顧客の移行とエイリアスの統合、ホスティングでの運用、
上記3ストア以外のストアはv0.1のスコープ外です。境界の全体は
[v0.1スコープ](docs/v0.1-scope.md)を参照してください。

<a id="sdk-roadmap"></a>

## SDKロードマップ

v0.1リリースのスコープに含まれるモバイルクライアントは、引き続きFlutterのみです。
プロバイダー非依存のHTTPクライアントに加え、Apple、Google Play、Huaweiの
コンパニオンパッケージで構成されます。追加のSDKコードは、
[#85](https://github.com/imariman/iapstack/issues/85)の拡張計画に沿ってすでにリポジトリにあります。
ただし、それによってv0.1のリリースゲートが広がることも、これらのパッケージが
本番リリースになることもありません。

| SDK | リポジトリに実装済みの内容 | 残りの作業 |
| --- | --- | --- |
| [Go信頼済みホスト](sdk/go/README.md) | 顧客セッション、サーバーサイドでのエンタイトルメント照会、署名付きWebhookの処理 | バージョン付きモジュールの初回公開（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [TypeScript信頼済みホスト](sdk/typescript/README.md) | 顧客セッション、エンタイトルメント照会、署名付きWebhook、インストール可能なESM/型アーカイブ | npmへの初回公開（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [iOS（Swift）](sdk/ios/README.md) | HTTPクライアント、StoreKit 2コンパニオン、StoreKit構成付きの実行可能なサンプル、ルートのSwiftPMマニフェスト | 初回のSDKバージョンタグ（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [Android（Kotlin）](sdk/android/README.md) | HTTPクライアント、Play Billing/HMSアダプター、信頼済みホストのサンプル、ステージング済みのMavenバイナリ/ソースjar | GitHub Mavenへの初回公開（[#141](https://github.com/imariman/iapstack/issues/141)） |
| [React Native](sdk/react-native/README.md) | HTTPクライアント、ネイティブのStoreKit/Play/Huaweiブリッジ、ネイティブのサンプルアプリ、パッケージ化されたコンパニオンのソース | npmへの初回公開（[#141](https://github.com/imariman/iapstack/issues/141)） |

[SDKリリースガイド](docs/sdk-releases.md)では、インストール、独立したプレリリース
バージョン、パッケージのチェック、レジストリの設定を説明しています。このリポジトリに
コードがあっても、レジストリでリリース済みであることを意味するものではありません。

永続的なアプリケーション用ベアラートークンは信頼済みホストのバックエンドに置き、
モバイルバイナリやブラウザバンドルには決して含めないでください。モバイルクライアントは
有効期間の短い顧客セッションを使用します。

<a id="quick-start"></a>

## クイックスタート

Compose対応のDockerとOpenSSLが必要です。このリポジトリのクローンで、ローカル専用の
シークレットを生成し、スタック一式を起動します。

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

生成した値は安全に保管し、このComposeボリュームで再利用してください。値を再生成しても、
既存のデータベースパスワードや保護キーはローテーションされません。

次に`http://127.0.0.1:8080/dashboard/`を開き、ブートストラップ用の管理者キーで接続します。
永続的な管理者キーを作成して保管し、環境から`IAPSTACK_BOOTSTRAP_ADMIN_KEY`を削除して、
APIコンテナを再作成してください。[運用ガイド](docs/operations.md)では、ブートストラップ、
キーのローテーション、シャットダウン、バックアップ、アップグレード、本番環境のセキュリティ
ベースラインを説明しています。

<a id="deploy"></a>

## デプロイ

プロバイダーのリソースを作成する前に、[デプロイターゲット一覧](deploy/README.md)で
アカウント要件、無料枠の制限、スタック一式のコストを比較してください。

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| ターゲット | 構成 | ガイド |
| --- | --- | --- |
| Render | APIとワーカーを実行する無料のWebサービス1つ、テスト用の無料PostgreSQLデータベース、有料のコンパクト構成または分割構成のBlueprint | [Renderガイド](deploy/render/README.md) |
| Heroku | 常時稼働のBasic dyno 2つで動くAPI、ワーカー、マネージドPostgreSQL。無料のランタイムはありません | [Herokuガイド](deploy/heroku/README.md) |
| DigitalOcean | ワーカーとマイグレーションジョブを含むApp Platform、または低コストのDroplet | [App Platformガイド](deploy/digitalocean/README.md) · [Dropletガイド](deploy/digitalocean/droplet.md) |
| Fly.io | プライベートなManaged Postgresを使うプロセスグループ。無料なのは短期間のトライアルのみ | [Fly.ioガイド](deploy/fly/README.md) |
| Koyeb | 公式CLIによるマネージドデータベース、HTTPS API、プライベートワーカー | [Koyebガイド](deploy/koyeb/README.md) |
| Railway | PostgreSQLと、APIとワーカーを実行する1つのサービス。Hobbyプランで月額約USD 5 | [Railwayガイド](deploy/railway/README.md) |
| Coolify | リポジトリ管理のDocker Compose定義から、自分のサーバー上にスタック一式を構築 | [Coolifyガイド](deploy/coolify/README.md) |
| 単一VM | 常時稼働で最も安価な選択肢：Cloudflare Tunnelの背後にある1台のLinuxマシンで、PostgreSQL、マイグレーション、API、ワーカーを実行 | [単一VMガイド](deploy/vps/README.md) |

### 最低コストの選択肢：単一VM

1台のLinuxマシンで、[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml)からすべてを
実行します。構成は、プライベートなDockerネットワーク上のPostgreSQL、1回限りの
マイグレーションコンテナ、APIと永続ワーカーを含む1つのコンパクトなプロセス、
Cloudflare Tunnelコネクターです。そのため、VMで受信ポートを開ける必要はありません。
ワーカーには常に動き続けるプロセスが必要なため、ゼロまでスケールするプラットフォームより、
常時稼働の小さなVMのほうが適しています。

| ホスト | 一般的なコスト | 適性 |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | 最大4 Arm OCPU、24 GB RAM。ワンクリックのResource Managerスタック。 |
| Google Cloud e2-micro（Free Tier、米国リージョン） | USD 0–4/月 | 低トラフィック向けの1 GB RAM。ガイド付きのCloud Shellウォークスルー。 |
| Hetzner Cloud CAX11 / CX22 | 約EUR 4–5/月 | 予測しやすい料金。1つのcloud-initファイルでインストール。 |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | 月額USD 6 / 12 | パブリックIPv4込み。Hetznerと同じcloud-initファイル。 |
| すでに運用しているサーバー | USD 0 | NAT配下でも動作しますが、ストア通知の受信には継続的な稼働が必要です。 |

また、DNSをCloudflareの無料プランで管理しているドメインが必要です。リソースを作成する前に、
各プロバイダーの最新の利用条件を確認してください。インストール手順、バックアップ、
アップグレードについては、[英語版READMEのDeployセクション](README.md#deploy)と
[単一VMガイド](deploy/vps/README.md)で説明しています。

<a id="design-principles"></a>

## 設計原則

**デフォルトでセルフホスト。** インフラ、認証情報、購入データ、アクセスの判定はすべてあなたのものです。

**ストアはアダプター。** ストア固有のAPIは共通のコントラクトの背後に隔離され、コアドメインは特定のマーケットプレイスに依存しません。

**エンタイトルメントがプロダクトの境界。** アプリケーションは生のレシートやストアの状態を解釈する代わりに、顧客が何にアクセスできるかを問い合わせます。

**どこにでもデプロイ。** 特定のクラウドプラットフォームに依存するのではなく、シンプルなコンテナデプロイを目標としています。

**独立した実装。** 動作とセキュリティ要件は、ストアの公式ドキュメントに基づいて定義しています。相互運用性やエッジケースのために他のオープンソースプロジェクトを参考にすることはありますが、IAPStackのコードは独自に実装しています。

<a id="documentation"></a>

## ドキュメント

ドキュメントは英語で書かれています。

- [v0.1のスコープとアーキテクチャ](docs/v0.1-scope.md)
- [アーキテクチャ決定記録（ADR）](docs/adr/README.md)
- [HTTP API v1とプロバイダー通知のコントラクト](docs/api-v1.md)
- [機械可読なOpenAPI v1コントラクト](contracts/openapi/v1.yaml)
- [運用とダッシュボードのガイド](docs/operations.md)
- [v0.1の脅威モデルとセキュリティリリースチェックリスト](docs/security.md)
- [セキュリティポリシーと脆弱性の報告](SECURITY.md)
- [コントリビューションガイド](CONTRIBUTING.md)
- [サポートガイド](SUPPORT.md)
- [プロバイダー非依存のFlutter SDK](sdk/flutter/iapstack/README.md)
- [ネイティブiOS Swift SDKとStoreKitコンパニオン](sdk/ios/README.md)
- [Go信頼済みホストSDK](sdk/go/README.md)
- [TypeScript信頼済みホストSDK](sdk/typescript/README.md)

<a id="development"></a>

## 開発

ソースからのビルド、設定、テスト、リポジトリ構成については、
[英語版READMEのDevelopmentセクション](README.md#development)で説明しています。
プルリクエストを作成する前に、[コントリビューションガイド](CONTRIBUTING.md)をお読みください。

<a id="roadmap"></a>

## ロードマップ

- [x] コアドメイン、公開APIのコントラクト、永続化モデルを定義する
- [x] Apple App Store、Google Play、Huawei AppGalleryの初期アダプターを実装する
- [x] PostgreSQLマイグレーションと、本番を想定したDocker構成を追加する
- [x] プロバイダー非依存のFlutter SDKと3つのプロバイダー別Flutter SDK、ダッシュボードを構築する
- [x] ライフサイクルの照合と署名付きの送信Webhookをサポートする
- [x] クライアント互換性チェック付きの機械可読なv1 APIコントラクトを公開する
- [x] Go、TypeScript、Swift、Kotlin、React NativeのSDKを実装する
- [x] 顧客の移行、エイリアスの統合、RevenueCatからのインポート、財務レポートのインポートをサポートする
- [x] Kubernetesのデプロイマニフェストを追加する
- [ ] Apple App Store、Google Play、Huaweiのライフサイクルのリリースゲートを完了し、`v0.1.0-rc.1`を公開する
- [ ] リリース候補へのフィードバックに対応し、安定版`v0.1.0`を公開する
- [ ] バージョン付きのホスト、Android、iOS、React Nativeパッケージを公開する（[#141](https://github.com/imariman/iapstack/issues/141)）
- [ ] Amazon Appstoreおよびその他のストアアダプターを追加する

完了済みの項目はリポジトリ内の実装を示すものであり、本番リリースの準備が整ったことを示すものではありません。

<a id="license"></a>

## ライセンス

[Apache License 2.0](LICENSE)のもとでライセンスされています。

このプロジェクトに参加することで、[行動規範](CODE_OF_CONDUCT.md)に従うことに同意したものとみなされます。
コントリビューションとサポートの方法については、[CONTRIBUTING.md](CONTRIBUTING.md)と
[SUPPORT.md](SUPPORT.md)を参照してください。
