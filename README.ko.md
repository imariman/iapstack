<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack 고퍼 마스코트" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>셀프 호스팅 인앱 구매 검증 및 엔타이틀먼트.</strong>
  <br>
  Apple App Store, Google Play, Huawei AppGallery를 위한, 백엔드가 소유하는 하나의 모델.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ko/">웹사이트</a>
  ·
  <a href="#quick-start">빠른 시작</a>
  ·
  <a href="#deploy">배포</a>
  ·
  <a href="#documentation">문서</a>
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
    · <strong>한국어</strong>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack 운영 대시보드: 데모 프로젝트의 구매 경로 상태, 마우스를 올린 날짜가 표시된 검증 활동, 집계된 스토어 매출, 샌드박스 테스트 금액" width="100%">
  </picture>
</p>

> [!NOTE]
> 이 문서는 [영어 README](README.md)의 번역본입니다. 두 문서의 내용이 다르면 영어판이 기준입니다.

IAPStack은 서버에서 인앱 구매를 검증하고 스토어 거래를 지속적인 엔타이틀먼트로
전환하는 백엔드이자 컨트롤 패널입니다. 앱은 *이 고객이 무엇에 접근할 수 있는가?*라는
하나의 질문만 하면 되고, 구매가 어느 스토어에서 이루어졌든 답은 같습니다. 자격 증명,
구매 데이터, 접근 결정은 직접 운영하는 인프라에 그대로 남습니다.

| 한 번 검증하고 어디서나 | 영수증이 아닌 엔타이틀먼트 | 직접 운영 |
| --- | --- | --- |
| 하나의 스토어 중립 모델 뒤에서 세 스토어를 서버 측에서 검증합니다. 스토어 API는 어댑터 안에 격리됩니다. | 앱은 원시 영수증과 스토어별 상태를 해석하는 대신 접근 상태를 읽습니다. | Go 바이너리 하나와 PostgreSQL로 구성됩니다. 어떤 클라우드에서든, 이미 보유한 VM에서든 Docker로 실행됩니다. |

> [!IMPORTANT]
> IAPStack은 프리릴리스 소프트웨어이며 아직 프로덕션 사용을 지원하지 않습니다.
> `v0.1.0-rc1` 및 `v0.1.0-rc2` 릴리스는 레거시 프리뷰입니다. 처음으로 지원되는 후보
> 버전은 세 프로바이더 모두의 실제 스토어 라이프사이클 게이트를 통과한 뒤 출시됩니다.

<a id="features"></a>

## 기능

- Apple App Store, Google Play, Huawei AppGallery의 구독 및 비소모성 평생 구매
  **검증**
- 영속적인 스토어 알림 수집과 예약된 재조정을 통한 **라이프사이클 추적**
- 엔타이틀먼트가 바뀔 때마다 백엔드에 알려 주는 **서명된 아웃바운드 웹훅**
- 애플리케이션, 고객, 큐, 웹훅 전송을 관리하며 라이트 및 다크 테마를 지원하는
  **운영 대시보드**
- 기계 판독 가능한 [OpenAPI 계약](contracts/openapi/v1.yaml)을 갖춘 **버전이 지정된 HTTP API**
- Flutter, iOS, Android, React Native용 **클라이언트 SDK**와 자체 백엔드를 위한 Go 및
  TypeScript SDK
- **유연한 토폴로지**: 하나의 통합형(compact) 프로세스 또는 분리된 API 및 워커 서비스

소모성 상품 지급, 호스팅 운영, 위 세 곳 이외의 스토어는 v0.1 범위에 포함되지 않습니다.
고객 마이그레이션, RevenueCat 가져오기, 오퍼링, Apple 재무 보고서는 이미 `main`에 있으며
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1)에서
미리 사용해 볼 수 있지만, v0.1 릴리스 게이트에는 포함되지 않습니다. 전체 경계는
[v0.1 범위](docs/v0.1-scope.md)를 참고하세요.

<a id="quick-start"></a>

## 빠른 시작

Compose가 포함된 Docker와 OpenSSL이 필요합니다. 이 저장소를 클론한 위치에서 로컬
시크릿을 생성하고 스택을 시작합니다.

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

생성한 값은 보관해 두세요. Compose 볼륨이 이 값에 의존하며, 값을 새로 생성해도 기존
데이터베이스 비밀번호나 보호 키가 교체되지는 않습니다.

`http://127.0.0.1:8080/dashboard/`를 열고 "Use an admin or bootstrap key instead"를 선택한 뒤
부트스트랩 관리자 키로 로그인합니다. 설정 배너에서 관리자 계정을 만들면 이후에는 그
사용자 이름과 비밀번호로 로그인합니다. 그런 다음 `IAPSTACK_BOOTSTRAP_ADMIN_KEY`를
제거하고 API 컨테이너를 다시 생성하세요. 키 교체, 백업, 업그레이드, 프로덕션 보안 기준 등 나머지 내용은
[운영 가이드](docs/operations.md)에서 다룹니다.

<a id="deploy"></a>

## 배포

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

모든 대상은 PostgreSQL, 마이그레이션 단계, API, 그리고 계속 실행되어야 하는 워커와 함께
동일한 컨테이너 이미지를 실행합니다. 리소스를 만들기 전에
[배포 대상 비교표](deploy/README.md)에서 계정 요구 사항, 무료 등급 제한, 전체 스택 비용을
비교하세요.

| 대상 | 구성 | 가이드 |
| --- | --- | --- |
| **단일 VM** (Oracle, Google Cloud, Hetzner, DigitalOcean, 자체 서버) | 가장 저렴한 상시 운영 옵션: 열린 인바운드 포트 없이 Cloudflare Tunnel 뒤의 Linux 머신 한 대에서 전체 스택을 실행합니다. Oracle Cloud Always Free에서는 월 USD 0부터 시작합니다. | [단일 VM 가이드](deploy/vps/README.md) |
| Render | 평가용 무료 통합형 서비스와 일회용 PostgreSQL, 유료 통합형 또는 분리형 Blueprint | [Render](deploy/render/README.md) |
| Railway | PostgreSQL과 API/워커 서비스 하나. 대개 USD 5 Hobby 플랜 범위 내 | [Railway](deploy/railway/README.md) |
| Heroku | Basic dyno 두 개에서 API, 워커, 관리형 PostgreSQL 실행. 무료 등급 없음 | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | App Spec으로 구성하는 API, 워커, 마이그레이션 작업 | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | 프라이빗 Managed Postgres를 사용하는 프로세스 그룹 | [Fly.io](deploy/fly/README.md) |
| Koyeb | CLI를 통한 관리형 데이터베이스, HTTPS API, 프라이빗 워커 | [Koyeb](deploy/koyeb/README.md) |
| Coolify | 저장소의 Compose 정의로 자체 서버에 전체 스택 배포 | [Coolify](deploy/coolify/README.md) |
| Kubernetes | API, 워커, 마이그레이션 작업을 위한 일반 매니페스트. PostgreSQL은 직접 준비 | [Kubernetes](deploy/k8s/README.md) |

0까지 축소되는(scale-to-zero) 플랫폼과 서버리스 플랫폼은 적합하지 않습니다. 워커가 스토어
알림을 수집하고, 웹훅을 전송하며, 매분 재조정을 실행하기 때문입니다.

<a id="sdks"></a>

## SDK

모바일 앱은 수명이 짧은 고객 세션으로 IAPStack과 통신합니다. 장기 유효 애플리케이션 키는
백엔드에 두어야 하며, 모바일 바이너리나 브라우저 번들에 절대 포함해서는 안 됩니다.

| 플랫폼 | 패키지 | 상태 |
| --- | --- | --- |
| Flutter | [프로바이더 중립 클라이언트](sdk/flutter/iapstack/README.md)와 [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md), [Huawei](sdk/flutter/iapstack_huawei/README.md) 컴패니언 | v0.1 릴리스 범위 |
| iOS (Swift) | [HTTP 클라이언트 및 StoreKit 2 컴패니언](sdk/ios/README.md) | 구현 완료, 아직 게시되지 않음 |
| Android (Kotlin) | [Play Billing 및 HMS 어댑터를 포함한 HTTP 클라이언트](sdk/android/README.md) | 구현 완료, 아직 게시되지 않음 |
| React Native | [HTTP 클라이언트 및 네이티브 StoreKit/Play/Huawei 브리지](sdk/react-native/README.md) | 구현 완료, 아직 게시되지 않음 |
| Go (백엔드) | [고객 세션, 엔타이틀먼트 조회, 웹훅 검증](sdk/go/README.md) | 구현 완료, 아직 게시되지 않음 |
| TypeScript (백엔드) | [고객 세션, 엔타이틀먼트 조회, 웹훅 검증](sdk/typescript/README.md) | 구현 완료, 아직 게시되지 않음 |

레지스트리 게시는 [#141](https://github.com/imariman/iapstack/issues/141)에서 추적합니다.
[SDK 릴리스 가이드](docs/sdk-releases.md)에서 소스 설치, 버전 관리, 릴리스 자동화를
다룹니다.

<a id="documentation"></a>

## 문서

문서는 영어로 작성되어 있습니다.

| 가이드 | 다루는 내용 |
| --- | --- |
| [운영](docs/operations.md) | 시크릿, 구성, 대시보드, 스토어 알림 설정, 백업, 업그레이드, 릴리스 |
| [HTTP API v1](docs/api-v1.md) | 엔드포인트, 프로바이더 자격 증명, 알림, 웹훅 검증 |
| [v0.1 범위 및 아키텍처](docs/v0.1-scope.md) | 시스템 컨텍스트, 핵심 불변 조건, 프로바이더 슬라이스 |
| [보안](docs/security.md) | 위협 모델 및 릴리스 보안 체크리스트 |
| [아키텍처 결정](docs/adr/README.md) | 주요 경계가 존재하는 이유 |
| [배포 대상](deploy/README.md) | 프로바이더 비교 및 프로바이더별 가이드 |
| [릴리스 런북](docs/releases/v0.1.0-rc.1.md) | 세 프로바이더의 실제 스토어 릴리스 게이트 |

<a id="development"></a>

## 개발

`go.mod`에 선언된 Go 툴체인을 사용하세요. PostgreSQL을 시작하고 마이그레이션을 적용한 뒤
API와 워커를 하나의 프로세스로 실행합니다.

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

서버에는 [빠른 시작](#quick-start)의 보호 키도 필요합니다. 모든 설정은
[구성 레퍼런스](docs/operations.md#configuration-reference)에 정리되어 있습니다. API는
`:8080`에서 요청을 받고, `/healthz`와 `/readyz`를 제공하며, `/dashboard/`에 대시보드를
내장합니다.

서버 검사와 엔드투엔드 릴리스 게이트를 실행합니다.

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

[기여 가이드](CONTRIBUTING.md)에는 SDK 검사, 통합 테스트 설정, 아키텍처 개요, 코드 규칙이
정리되어 있습니다.

<a id="repository-layout"></a>

### 저장소 구조

```text
.
├── cmd/iapstack/       # API, 워커, 마이그레이션 진입점
├── internal/
│   ├── core/           # 고객, 거래, 상품, 엔타이틀먼트
│   ├── verification/   # 프로바이더 중립 구매 검증
│   ├── stores/         # Apple, Google Play, Huawei 어댑터
│   ├── credentials/    # 보호된 애플리케이션 자격 증명
│   ├── protection/     # 민감한 데이터 보호 포트
│   ├── persistence/    # PostgreSQL 리포지토리
│   └── platform/       # 구성, 로깅, 키링 구현
├── dashboard/          # 내장 운영 대시보드
├── contracts/          # OpenAPI 및 웹훅 계약
├── sdk/                # Flutter, iOS, Android, React Native, Go, TypeScript SDK
├── deploy/             # Compose, 클라우드, VM, Kubernetes 정의
└── docs/               # 가이드, ADR, 릴리스 런북
```

<a id="roadmap"></a>

## 로드맵

- [x] 스토어 중립 도메인, PostgreSQL 영속성, 버전이 지정된 OpenAPI 계약
- [x] Apple App Store, Google Play, Huawei AppGallery 어댑터
- [x] 라이프사이클 재조정 및 서명된 아웃바운드 웹훅
- [x] 운영 대시보드 및 클라우드, VM, Kubernetes용 배포 정의
- [x] Flutter, Swift, Kotlin, React Native, Go, TypeScript SDK
- [x] 고객 마이그레이션, RevenueCat 가져오기, 오퍼링, Apple 재무 보고서 (`v0.2.0-alpha.1`)
- [x] 구독 지표, 고객 지원 도구, Apple 환불 소비 정보 응답 (`main`에 반영, 아직 미출시)
- [ ] 세 프로바이더의 실제 스토어 게이트를 통과하고 `v0.1.0-rc.1` 게시
- [ ] 안정 버전 `v0.1.0` 게시
- [ ] 버전이 지정된 SDK 패키지 게시 ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] AI 에이전트가 최소 권한 키로 IAPStack을 조회하고 운영할 수 있는 MCP 엔드포인트 ([#285](https://github.com/imariman/iapstack/issues/285))
- [ ] Google Play 및 Huawei 재무 보고서 인식
- [ ] Amazon Appstore 및 추가 스토어 어댑터

완료된 항목은 저장소에 구현되었음을 의미하며, 프로덕션 준비가 되었다는 뜻은 아닙니다.

<a id="contributing"></a>

## 기여하기

버그 신고, 수정, 스토어 관련 전문 지식을 환영합니다. 풀 리퀘스트를 열기 전에
[기여 가이드](CONTRIBUTING.md)를, 질문이 있으면 [지원 가이드](SUPPORT.md)를, 취약점을
신고하려면 [보안 정책](SECURITY.md)을 읽어 주세요. 프로젝트 참여에는
[행동 강령](CODE_OF_CONDUCT.md)이 적용됩니다.

<a id="license"></a>

## 라이선스

[Apache License 2.0](LICENSE)
