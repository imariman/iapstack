<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack 고퍼 마스코트" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>오픈 소스, 셀프 호스팅 인앱 구매 검증 및 엔타이틀먼트.</strong>
  <br>
  Apple App Store, Google Play, Huawei AppGallery를 위한, 백엔드가 소유하는 하나의 모델.
  <br>
  Go로 개발. 스토어 중립. 어디에나 배포 가능.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ko/"><strong>웹사이트</strong></a>
  ·
  <a href="#quick-start"><strong>빠른 시작</strong></a>
  ·
  <a href="#deploy"><strong>배포</strong></a>
  ·
  <a href="#documentation"><strong>문서</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>기여하기</strong></a>
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
  · <strong>한국어</strong>
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
    <img src="assets/dashboard-light.png" alt="IAPStack 운영 대시보드: 데모 프로젝트의 구매 경로 상태, 마우스를 올린 날짜가 표시된 검증 활동, 집계된 스토어 매출, 샌드박스 테스트 금액" width="100%">
  </picture>
</p>

<p align="center">
  <sub>시스템 테마를 따르는 운영 대시보드입니다(제품에서는 시스템 / 라이트 / 다크 선택 가능). 합성 데모 데이터로 표시했습니다.</sub>
</p>

> [!NOTE]
> 이 문서는 [영어 README](README.md)의 번역본입니다. 두 문서의 내용이 다르면 영어판이 기준입니다. 개발, 저장소 구조, 단계별 배포 세부 사항은 영어로만 관리합니다.

> [!IMPORTANT]
> IAPStack은 프리릴리스 소프트웨어입니다. 기존 `v0.1.0-rc1` 및 `v0.1.0-rc2`
> 릴리스는 현재의 릴리스 게이트와 보안 수정 이전에 나온 레거시 프리뷰이며,
> 프로덕션 사용을 지원하지 않습니다. 기본 브랜치는 계속 개발 중이며, 세 프로바이더의
> 실제 스토어 라이프사이클 게이트는 아직 완료되지 않았습니다.

IAPStack은 인앱 구매를 검증하고 스토어 거래를 지속적인 애플리케이션 엔타이틀먼트로
전환하는 셀프 호스팅 백엔드이자 컨트롤 패널입니다. 자격 증명, 구매 데이터, 접근
결정을 팀이 직접 관리하는 인프라에 두면서, 여러 모바일 스토어에 걸쳐 백엔드가
소유하는 하나의 모델을 제공합니다.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| 한 번 검증하고 어디서나 | 영수증이 아닌 엔타이틀먼트 | 직접 운영 |
| --- | --- | --- |
| 하나의 스토어 중립 모델 뒤에서 Apple App Store, Google Play, Huawei AppGallery를 서버 측에서 검증합니다. | 앱은 원시 영수증과 스토어 상태를 해석하는 대신 고객이 무엇에 접근할 수 있는지 묻습니다. | 기본적으로 셀프 호스팅: 인프라, 자격 증명, 구매 데이터, 접근 결정을 직접 소유합니다. |

<a id="current-scope"></a>

## 현재 범위

- Apple App Store, Google Play, Huawei AppGallery의 구독 및 비소모성 평생 구매에 대한
  서버 측 검증
- PostgreSQL 기반의 스토어 중립적인 거래 및 엔타이틀먼트 모델
- 영속적인 알림 수집, 라이프사이클 재조정, 서명된 아웃바운드 웹훅
- 인증이 필요한 운영 대시보드(시스템 / 라이트 / 다크 테마, 접을 수 있는 프로젝트 레일,
  세부 내용을 확인할 수 있는 활동 및 구독 차트)와 버전이 지정된 HTTP/OpenAPI 계약
- 현재 세 프로바이더 모두를 위한 프로바이더 중립 및 스토어별 Flutter 패키지
- Docker와 문서화된 클라우드 배포 대상을 이용한 통합형(compact) 또는 분리형 API/워커 배포

소모성 상품 지급, 고객 마이그레이션 및 별칭 통합, 호스팅 운영, 위의 세 곳 이외의
스토어는 v0.1 범위에 포함되지 않습니다. 전체 경계는
[v0.1 범위](docs/v0.1-scope.md)를 참고하세요.

<a id="sdk-roadmap"></a>

## SDK 로드맵

v0.1 릴리스 범위의 모바일 클라이언트는 여전히 Flutter뿐입니다. 프로바이더 중립
HTTP 클라이언트와 Apple, Google Play, Huawei 컴패니언으로 구성됩니다. 추가 SDK 코드는
[#85](https://github.com/imariman/iapstack/issues/85)의 확장 계획에 따라 이미 저장소에
있습니다. 이 코드가 있다고 해서 v0.1 릴리스 게이트가 확장되거나 해당 패키지가 프로덕션
릴리스가 되는 것은 아닙니다.

| SDK | 저장소에 구현된 내용 | 남은 작업 |
| --- | --- | --- |
| [Go 신뢰 호스트](sdk/go/README.md) | 고객 세션, 서버 측 엔타이틀먼트 조회, 서명된 웹훅 처리 | 첫 번째 버전 모듈 게시 ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript 신뢰 호스트](sdk/typescript/README.md) | 고객 세션, 엔타이틀먼트 조회, 서명된 웹훅, 설치 가능한 ESM/타입 아카이브 | 첫 npm 게시 ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | HTTP 클라이언트, StoreKit 2 컴패니언, StoreKit 구성이 포함된 실행 가능한 샘플, 루트 SwiftPM 매니페스트 | 첫 SDK 버전 태그 ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | HTTP 클라이언트, Play Billing/HMS 어댑터, 신뢰 호스트 예제, 스테이징된 Maven 바이너리/소스 jar | 첫 GitHub Maven 게시 ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | HTTP 클라이언트, 네이티브 StoreKit/Play/Huawei 브리지, 네이티브 예제 앱, 패키징된 컴패니언 소스 | 첫 npm 게시 ([#141](https://github.com/imariman/iapstack/issues/141)) |

[SDK 릴리스 가이드](docs/sdk-releases.md)에는 설치, 독립적인 프리릴리스 버전, 패키지
검사, 레지스트리 설정이 정리되어 있습니다. 이 저장소에 코드가 있다고 해서 레지스트리에
릴리스가 존재한다는 의미는 아닙니다.

장기 유효 애플리케이션 베어러 토큰은 신뢰할 수 있는 호스트 백엔드에만 두어야 하며,
모바일 바이너리나 브라우저 번들에 절대 포함해서는 안 됩니다. 모바일 클라이언트는 수명이
짧은 고객 세션을 사용합니다.

<a id="quick-start"></a>

## 빠른 시작

Compose가 포함된 Docker와 OpenSSL이 필요합니다. 이 저장소를 클론한 위치에서 로컬 전용
시크릿을 생성하고 전체 스택을 시작합니다.

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

생성한 값은 안전하게 보관하고 이 Compose 볼륨과 함께 계속 사용하세요. 값을 다시
생성해도 기존 데이터베이스 비밀번호나 보호 키가 교체되지는 않습니다.

그다음 `http://127.0.0.1:8080/dashboard/`를 열고 부트스트랩 관리자 키로 연결합니다.
장기 유효 관리자 키를 만들어 보관한 뒤, 환경에서 `IAPSTACK_BOOTSTRAP_ADMIN_KEY`를
제거하고 API 컨테이너를 다시 생성하세요. [운영 가이드](docs/operations.md)에서
부트스트랩, 키 교체, 종료, 백업, 업그레이드, 프로덕션 보안 기준을 설명합니다.

<a id="deploy"></a>

## 배포

프로바이더 리소스를 만들기 전에 [배포 대상 비교표](deploy/README.md)에서 계정 요구 사항,
무료 등급 제한, 전체 스택 비용을 비교하세요.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| 대상 | 구성 | 가이드 |
| --- | --- | --- |
| Render | API와 워커를 실행하는 무료 웹 서비스 하나, 테스트용 무료 PostgreSQL 데이터베이스, 유료 통합형 또는 분리형 Blueprint | [Render 가이드](deploy/render/README.md) |
| Heroku | 상시 실행되는 Basic dyno 두 개에서 API, 워커, 관리형 PostgreSQL 실행. 무료 런타임 없음 | [Heroku 가이드](deploy/heroku/README.md) |
| DigitalOcean | 워커와 마이그레이션 작업을 포함한 App Platform, 또는 더 저렴한 Droplet | [App Platform 가이드](deploy/digitalocean/README.md) · [Droplet 가이드](deploy/digitalocean/droplet.md) |
| Fly.io | 프라이빗 Managed Postgres를 사용하는 프로세스 그룹. 짧은 무료 체험만 제공 | [Fly.io 가이드](deploy/fly/README.md) |
| Koyeb | 공식 CLI를 통한 관리형 데이터베이스, HTTPS API, 프라이빗 워커 | [Koyeb 가이드](deploy/koyeb/README.md) |
| Railway | PostgreSQL과 API 및 워커를 실행하는 서비스 하나. Hobby 플랜 기준 월 약 USD 5 | [Railway 가이드](deploy/railway/README.md) |
| Coolify | 저장소에서 관리하는 Docker Compose 정의로 자체 서버에 전체 스택 배포 | [Coolify 가이드](deploy/coolify/README.md) |
| 단일 VM | 가장 저렴한 상시 운영 옵션: Cloudflare Tunnel 뒤의 Linux 머신 한 대에서 PostgreSQL, 마이그레이션, API, 워커 실행 | [단일 VM 가이드](deploy/vps/README.md) |

### 최저 비용 옵션: 단일 VM

Linux 머신 한 대가 [`deploy/vps/compose.yaml`](deploy/vps/compose.yaml)로 모든 것을
실행합니다. 프라이빗 Docker 네트워크의 PostgreSQL, 1회성 마이그레이션 컨테이너, API와
영속 워커를 담은 통합 프로세스 하나, Cloudflare Tunnel 커넥터로 구성되므로 VM에 열린
인바운드 포트가 필요 없습니다. 워커에는 계속 실행되는 프로세스가 필요하므로, 0까지
축소되는(scale-to-zero) 플랫폼보다 항상 켜져 있는 작은 VM이 더 적합합니다.

| 호스트 | 일반적인 비용 | 적합성 |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | 최대 Arm OCPU 4개, RAM 24 GB. 원클릭 Resource Manager 스택. |
| Google Cloud e2-micro (Free Tier, US 리전) | USD 0–4/월 | 트래픽이 적은 경우 RAM 1 GB. 안내형 Cloud Shell 연습. |
| Hetzner Cloud CAX11 / CX22 | 약 EUR 4–5/월 | 예측 가능한 요금. cloud-init 파일 하나로 설치. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | 월 USD 6 / 12 | 공인 IPv4 포함. Hetzner와 같은 cloud-init 파일 사용. |
| 이미 운영 중인 서버 | USD 0 | NAT 뒤에서도 동작하지만, 스토어 알림을 받으려면 지속적인 가동이 필요합니다. |

DNS가 Cloudflare 무료 플랜에 있는 도메인도 필요합니다. 리소스를 만들기 전에 각
프로바이더의 최신 약관을 확인하세요. 설치 단계, 백업, 업그레이드는
[영어 README의 Deploy 섹션](README.md#deploy)과
[단일 VM 가이드](deploy/vps/README.md)에 설명되어 있습니다.

<a id="design-principles"></a>

## 설계 원칙

**기본적으로 셀프 호스팅.** 인프라, 자격 증명, 구매 데이터, 접근 결정을 직접 소유합니다.

**스토어는 어댑터입니다.** 스토어별 API는 공통 계약 뒤에 격리되며, 핵심 도메인은 특정 마켓플레이스 하나에 의존하지 않습니다.

**엔타이틀먼트가 제품의 경계입니다.** 애플리케이션은 원시 영수증이나 스토어 상태를 해석하는 대신 고객이 무엇에 접근할 수 있는지 묻습니다.

**어디에나 배포.** 특정 클라우드 플랫폼에 의존하지 않고, 단순한 컨테이너 배포를 목표로 합니다.

**독립적인 구현.** 동작과 보안 요구 사항은 공식 스토어 문서를 기준으로 정의합니다. 상호 운용성과 엣지 케이스를 위해 다른 오픈 소스 프로젝트를 참고할 수 있지만, IAPStack의 코드는 독립적으로 구현됩니다.

<a id="documentation"></a>

## 문서

문서는 영어로 작성되어 있습니다.

- [v0.1 범위 및 아키텍처](docs/v0.1-scope.md)
- [아키텍처 결정 기록(ADR)](docs/adr/README.md)
- [HTTP API v1 및 프로바이더 알림 계약](docs/api-v1.md)
- [기계 판독 가능한 OpenAPI v1 계약](contracts/openapi/v1.yaml)
- [운영 및 대시보드 가이드](docs/operations.md)
- [v0.1 위협 모델 및 보안 릴리스 체크리스트](docs/security.md)
- [보안 정책 및 취약점 신고](SECURITY.md)
- [기여 가이드](CONTRIBUTING.md)
- [지원 가이드](SUPPORT.md)
- [프로바이더 중립 Flutter SDK](sdk/flutter/iapstack/README.md)
- [네이티브 iOS Swift SDK 및 StoreKit 컴패니언](sdk/ios/README.md)
- [Go 신뢰 호스트 SDK](sdk/go/README.md)
- [TypeScript 신뢰 호스트 SDK](sdk/typescript/README.md)

<a id="development"></a>

## 개발

소스 빌드, 구성, 테스트, 저장소 구조는
[영어 README의 Development 섹션](README.md#development)에 설명되어 있습니다. 풀
리퀘스트를 열기 전에 [기여 가이드](CONTRIBUTING.md)를 읽어 주세요.

<a id="roadmap"></a>

## 로드맵

- [x] 핵심 도메인, 공개 API 계약, 영속성 모델 정의
- [x] 초기 Apple App Store, Google Play, Huawei AppGallery 어댑터 구현
- [x] PostgreSQL 마이그레이션과 프로덕션 형태의 Docker 구성 추가
- [x] 프로바이더 중립 Flutter SDK, 세 가지 프로바이더별 Flutter SDK, 대시보드 구축
- [x] 라이프사이클 재조정 및 서명된 아웃바운드 웹훅 지원
- [x] 클라이언트 호환성 검사를 포함한 기계 판독 가능한 v1 API 계약 게시
- [x] Go, TypeScript, Swift, Kotlin, React Native SDK 구현
- [x] 고객 마이그레이션, 별칭 통합, RevenueCat 가져오기, 재무 보고서 가져오기 지원
- [x] Kubernetes 배포 매니페스트 추가
- [ ] Apple App Store, Google Play, Huawei 라이프사이클 릴리스 게이트를 완료하고 `v0.1.0-rc.1` 게시
- [ ] 후보 버전 피드백을 반영하고 안정 버전 `v0.1.0` 게시
- [ ] 버전이 지정된 호스트, Android, iOS, React Native 패키지 게시 ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Amazon Appstore 및 추가 스토어 어댑터 추가

완료된 항목은 저장소에 구현되었음을 의미하며, 프로덕션 릴리스 준비가 되었다는 뜻은 아닙니다.

<a id="license"></a>

## 라이선스

[Apache License 2.0](LICENSE)에 따라 라이선스가 부여됩니다.

이 프로젝트에 참여하면 [행동 강령](CODE_OF_CONDUCT.md)을 따르는 데 동의하는 것으로
간주됩니다. 기여 및 지원 경로는 [CONTRIBUTING.md](CONTRIBUTING.md)와
[SUPPORT.md](SUPPORT.md)를 참고하세요.
