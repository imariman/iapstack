<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStackのGopherマスコット" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>セルフホスト型の、アプリ内課金の検証とエンタイトルメント管理。</strong>
  <br>
  Apple App Store、Google Play、Huawei AppGalleryを、バックエンドが管理する1つのモデルで扱います。
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ja/">ウェブサイト</a>
  ·
  <a href="#quick-start">クイックスタート</a>
  ·
  <a href="#deploy">デプロイ</a>
  ·
  <a href="#documentation">ドキュメント</a>
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
    · <strong>日本語</strong>
    · <a href="README.ko.md">한국어</a>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack運用ダッシュボード：デモプロジェクトの購入ルートの状態、ホバー中の1日を含む検証アクティビティ、計上されたストア収益、サンドボックスのテスト金額" width="100%">
  </picture>
</p>

> [!NOTE]
> これは[英語版README](README.md)の翻訳です。両者に相違がある場合は英語版が正となります。

IAPStackは、アプリ内課金をサーバー側で検証し、ストアのトランザクションを永続的な
エンタイトルメントに変換するバックエンド兼コントロールパネルです。アプリが問い合わせるのは
「*この顧客は何にアクセスできるか*」という1つの問いだけで、購入がどのストアで行われても
答えは同じです。認証情報、購入データ、アクセスの判定は、自分たちが運用するインフラに
とどまります。

| 一度の検証で、すべてのストアに | レシートではなくエンタイトルメント | 自分で運用できる |
| --- | --- | --- |
| 3つのストアのサーバーサイド検証を、ストアに依存しない1つのモデルの背後で行います。ストアのAPIはアダプター内に隔離されています。 | アプリは生のレシートやストア固有のステータスを解釈する代わりに、アクセス状態を読み取ります。 | 単一のGoバイナリとPostgreSQLだけで動きます。任意のクラウドのDocker上でも、手持ちのVM上でも実行できます。 |

> [!IMPORTANT]
> IAPStackはプレリリースソフトウェアであり、本番環境での使用はまだサポートしていません。
> `v0.1.0-rc1`と`v0.1.0-rc2`のリリースはレガシープレビューです。最初のサポート対象の
> リリース候補は、3プロバイダーすべてで実ストアのライフサイクルゲートを通過した時点で公開されます。

<a id="features"></a>

## 機能

- Apple App Store、Google Play、Huawei AppGalleryのサブスクリプションと
  非消耗型の買い切り購入の**検証**
- 永続的なストア通知の取り込みと定期的な照合による**ライフサイクルの追跡**
- **署名付きの送信Webhook**で、エンタイトルメントのあらゆる変更をバックエンドに通知
- アプリケーション、顧客、キュー、Webhook配信を扱う、ライト/ダークテーマ対応の
  **運用ダッシュボード**
- 機械可読な[OpenAPIコントラクト](contracts/openapi/v1.yaml)を備えた**バージョン管理されたHTTP API**
- Flutter、iOS、Android、React Native向けの**クライアントSDK**と、自社バックエンド向けの
  GoおよびTypeScript SDK
- **柔軟な構成**：コンパクトな単一プロセス、またはAPIとワーカーの分割サービス

消耗型アイテムの付与、ホスティングでの運用、上記3ストア以外のストアはv0.1のスコープ外です。
顧客の移行、RevenueCatからのインポート、オファリング、Appleの財務レポートはすでに`main`にあり、
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1)で
プレビューとして提供されていますが、v0.1のリリースゲートには含まれません。境界の全体は
[v0.1スコープ](docs/v0.1-scope.md)を参照してください。

<a id="quick-start"></a>

## クイックスタート

Compose対応のDockerとOpenSSLが必要です。このリポジトリのクローンで、ローカル用の
シークレットを生成し、スタックを起動します。

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

これらの値は保管しておいてください。Composeボリュームはこれらの値に依存しており、
新しい値を生成しても既存のデータベースパスワードや保護キーはローテーションされません。

`http://127.0.0.1:8080/dashboard/`を開き、ブートストラップ用の管理者キーでサインインします。
永続的な管理者キーを作成したら、`IAPSTACK_BOOTSTRAP_ADMIN_KEY`を削除してAPIコンテナを
再作成してください。キーのローテーション、バックアップ、アップグレード、本番環境の
セキュリティベースラインなど、その他の内容は[運用ガイド](docs/operations.md)で説明しています。

<a id="deploy"></a>

## デプロイ

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

どのターゲットでも、同じコンテナイメージでPostgreSQL、マイグレーションステップ、API、
常時稼働が必要なワーカーを実行します。リソースを作成する前に、
[デプロイターゲット一覧](deploy/README.md)でアカウント要件、無料枠の制限、
スタック一式のコストを比較してください。

| ターゲット | 構成 | ガイド |
| --- | --- | --- |
| **単一VM**（Oracle、Google Cloud、Hetzner、DigitalOcean、自前のサーバー） | 常時稼働で最も安価な選択肢：Cloudflare Tunnelの背後にある1台のLinuxマシンでスタック一式を実行し、受信ポートを開ける必要はありません。Oracle Cloud Always Freeなら月額USD 0から。 | [単一VMガイド](deploy/vps/README.md) |
| Render | 評価用の無料のコンパクトサービスと使い捨てのPostgreSQL。有料のコンパクト構成または分割構成のBlueprint | [Render](deploy/render/README.md) |
| Railway | PostgreSQLと、APIとワーカーを実行する1つのサービス。通常はUSD 5のHobbyプランに収まります | [Railway](deploy/railway/README.md) |
| Heroku | Basic dyno 2つで動くAPI、ワーカー、マネージドPostgreSQL。無料枠はありません | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | App SpecによるAPI、ワーカー、マイグレーションジョブ | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | プライベートなManaged Postgresを使うプロセスグループ | [Fly.io](deploy/fly/README.md) |
| Koyeb | CLIによるマネージドデータベース、HTTPS API、プライベートワーカー | [Koyeb](deploy/koyeb/README.md) |
| Coolify | リポジトリのCompose定義から、自分のサーバー上にスタック一式を構築 | [Coolify](deploy/coolify/README.md) |
| Kubernetes | API、ワーカー、マイグレーションジョブ用のプレーンなマニフェスト。PostgreSQLは各自で用意 | [Kubernetes](deploy/k8s/README.md) |

ゼロまでスケールするプラットフォームやサーバーレスプラットフォームは適していません。
ワーカーがストア通知の取り込み、Webhookの配信、毎分の照合を担っているためです。

<a id="sdks"></a>

## SDK

モバイルアプリは、有効期間の短い顧客セッションでIAPStackと通信します。永続的な
アプリケーションキーはバックエンドに置き、モバイルバイナリやブラウザバンドルには
決して含めないでください。

| プラットフォーム | パッケージ | ステータス |
| --- | --- | --- |
| Flutter | [プロバイダー非依存のクライアント](sdk/flutter/iapstack/README.md)と、[Apple](sdk/flutter/iapstack_apple/README.md)、[Google Play](sdk/flutter/iapstack_google_play/README.md)、[Huawei](sdk/flutter/iapstack_huawei/README.md)のコンパニオン | v0.1リリースのスコープ |
| iOS（Swift） | [HTTPクライアントとStoreKit 2コンパニオン](sdk/ios/README.md) | 実装済み、未公開 |
| Android（Kotlin） | [Play BillingおよびHMSアダプター付きのHTTPクライアント](sdk/android/README.md) | 実装済み、未公開 |
| React Native | [HTTPクライアントとネイティブのStoreKit/Play/Huaweiブリッジ](sdk/react-native/README.md) | 実装済み、未公開 |
| Go（バックエンド） | [顧客セッション、エンタイトルメント照会、Webhook検証](sdk/go/README.md) | 実装済み、未公開 |
| TypeScript（バックエンド） | [顧客セッション、エンタイトルメント照会、Webhook検証](sdk/typescript/README.md) | 実装済み、未公開 |

レジストリへの公開は[#141](https://github.com/imariman/iapstack/issues/141)で管理しています。
[SDKリリースガイド](docs/sdk-releases.md)では、ソースからのインストール、バージョニング、
リリースの自動化を説明しています。

<a id="documentation"></a>

## ドキュメント

ドキュメントは英語で書かれています。

| ガイド | 内容 |
| --- | --- |
| [運用](docs/operations.md) | シークレット、設定、ダッシュボード、ストア通知の設定、バックアップ、アップグレード、リリース |
| [HTTP API v1](docs/api-v1.md) | エンドポイント、プロバイダーの認証情報、通知、Webhook検証 |
| [v0.1のスコープとアーキテクチャ](docs/v0.1-scope.md) | システムコンテキスト、コアの不変条件、プロバイダーごとのスライス |
| [セキュリティ](docs/security.md) | 脅威モデルとリリースのセキュリティチェックリスト |
| [アーキテクチャ決定記録](docs/adr/README.md) | 主要な境界が存在する理由 |
| [デプロイターゲット](deploy/README.md) | プロバイダーの比較とプロバイダー別ガイド |
| [リリースランブック](docs/releases/v0.1.0-rc.1.md) | 3プロバイダーの実ストアによるリリースゲート |

<a id="development"></a>

## 開発

`go.mod`で宣言されたGoツールチェーンを使用します。PostgreSQLを起動し、マイグレーションを
適用して、APIとワーカーを1つのプロセスで実行します。

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

サーバーには[クイックスタート](#quick-start)の保護キーも必要です。すべての設定は
[設定リファレンス](docs/operations.md#configuration-reference)に一覧があります。
APIは`:8080`で待ち受け、`/healthz`と`/readyz`を提供し、`/dashboard/`にダッシュボードを
組み込んでいます。

サーバーのチェックとエンドツーエンドのリリースゲートを実行します。

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

[コントリビューションガイド](CONTRIBUTING.md)には、SDKのチェック、統合テストのセットアップ、
アーキテクチャの概要、コーディング規約がまとめられています。

<a id="repository-layout"></a>

### リポジトリ構成

```text
.
├── cmd/iapstack/       # API、ワーカー、マイグレーションのエントリーポイント
├── internal/
│   ├── core/           # 顧客、トランザクション、プロダクト、エンタイトルメント
│   ├── verification/   # プロバイダー非依存の購入検証
│   ├── stores/         # Apple、Google Play、Huaweiのアダプター
│   ├── credentials/    # 保護されたアプリケーション認証情報
│   ├── protection/     # 機密データ保護のポート
│   ├── persistence/    # PostgreSQLリポジトリ
│   └── platform/       # 設定、ロギング、キーリングの実装
├── dashboard/          # 組み込みの運用ダッシュボード
├── contracts/          # OpenAPIとWebhookのコントラクト
├── sdk/                # Flutter、iOS、Android、React Native、Go、TypeScriptのSDK
├── deploy/             # Compose、クラウド、VM、Kubernetesの定義
└── docs/               # ガイド、ADR、リリースランブック
```

<a id="roadmap"></a>

## ロードマップ

- [x] ストアに依存しないドメイン、PostgreSQLによる永続化、バージョン管理されたOpenAPIコントラクト
- [x] Apple App Store、Google Play、Huawei AppGalleryのアダプター
- [x] ライフサイクルの照合と署名付きの送信Webhook
- [x] 運用ダッシュボードと、クラウド、VM、Kubernetes向けのデプロイ定義
- [x] Flutter、Swift、Kotlin、React Native、Go、TypeScriptのSDK
- [x] 顧客の移行、RevenueCatからのインポート、オファリング、Appleの財務レポート（`v0.2.0-alpha.1`）
- [x] サブスクリプション指標、カスタマーサポートツール、Appleの返金に関する消費情報の回答（`main` に実装済み、未リリース）
- [ ] 3プロバイダーの実ストアゲートを通過し、`v0.1.0-rc.1`を公開する
- [ ] 安定版`v0.1.0`を公開する
- [ ] バージョン付きのSDKパッケージを公開する（[#141](https://github.com/imariman/iapstack/issues/141)）
- [ ] AIエージェントが最小権限のキーでIAPStackを照会・操作できるMCPエンドポイント（[#285](https://github.com/imariman/iapstack/issues/285)）
- [ ] Google PlayとHuaweiの財務レポートによる収益認識
- [ ] Amazon Appstoreおよびその他のストアアダプター

チェック済みの項目はリポジトリ内に実装されていることを示すものであり、本番環境で利用できる
状態であることを意味するものではありません。

<a id="contributing"></a>

## コントリビューション

バグ報告、修正、ストアに関する知見を歓迎します。プルリクエストを作成する前に
[コントリビューションガイド](CONTRIBUTING.md)を、質問については[サポートガイド](SUPPORT.md)を、
脆弱性の報告については[セキュリティポリシー](SECURITY.md)をお読みください。
参加には[行動規範](CODE_OF_CONDUCT.md)が適用されます。

<a id="license"></a>

## ライセンス

[Apache License 2.0](LICENSE)
