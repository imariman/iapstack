<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Маскот IAPStack — гофер" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Self-hosted проверка встроенных покупок и управление правами доступа.</strong>
  <br>
  Единая модель на стороне бэкенда для Apple App Store, Google Play и Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ru/">Сайт</a>
  ·
  <a href="#quick-start">Быстрый старт</a>
  ·
  <a href="#deploy">Развёртывание</a>
  ·
  <a href="#documentation">Документация</a>
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
    · <strong>Русский</strong>
    · <a href="README.ja.md">日本語</a>
    · <a href="README.ko.md">한국어</a>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="Операционная панель IAPStack: состояние маршрута покупок, активность проверок с выделенным при наведении днём, признанная выручка из магазинов и объём тестовых покупок в песочнице для демопроекта" width="100%">
  </picture>
</p>

> [!NOTE]
> Это перевод [README на английском языке](README.md). При расхождениях приоритет имеет английская версия.

IAPStack — это бэкенд и панель управления, которые проверяют встроенные покупки на сервере
и превращают транзакции магазинов в надёжно хранимые права доступа. Ваши приложения задают
один вопрос — *к чему у этого клиента есть доступ?* — и получают одинаковый ответ, из какого
бы магазина ни пришла покупка. Учётные данные, данные о покупках и решения о доступе
остаются на инфраструктуре, которой управляете вы.

| Одна проверка для всех магазинов | Права доступа, а не чеки | Под вашим управлением |
| --- | --- | --- |
| Серверная проверка для трёх магазинов за единой моделью, не зависящей от магазина. API магазинов изолированы в адаптерах. | Приложения читают состояние доступа, а не разбирают сырые чеки и статусы конкретных магазинов. | Один бинарный файл на Go и PostgreSQL. Работает в Docker в любом облаке или на уже имеющейся у вас VM. |

> [!IMPORTANT]
> IAPStack — предрелизное программное обеспечение, и использование в продакшене пока
> не поддерживается. Релизы `v0.1.0-rc1` и `v0.1.0-rc2` — устаревшие предварительные
> версии; первый поддерживаемый релиз-кандидат выйдет, когда для всех трёх провайдеров
> будут пройдены гейты жизненного цикла с реальными магазинами.

<a id="features"></a>

## Возможности

- **Проверка** подписок и непотребляемых бессрочных покупок из Apple App Store,
  Google Play и Huawei AppGallery
- **Отслеживание жизненного цикла** благодаря надёжному приёму уведомлений магазинов
  и плановой сверке
- **Подписанные исходящие вебхуки**, чтобы ваш бэкенд узнавал о каждом изменении прав доступа
- **Операционная панель** для приложений, клиентов, очередей и доставок вебхуков,
  со светлой и тёмной темами
- **Версионированный HTTP API** с машиночитаемым [контрактом OpenAPI](contracts/openapi/v1.yaml)
- **Клиентские SDK** для Flutter, iOS, Android и React Native, а также SDK для Go
  и TypeScript для вашего собственного бэкенда
- **Гибкая топология**: один компактный процесс или раздельные сервисы API и воркера

Выдача потребляемых покупок, работа в виде хостинга и магазины помимо этих трёх выходят
за рамки v0.1. Миграция клиентов, импорт из RevenueCat, предложения (offerings)
и финансовые отчёты Apple уже есть в `main` и доступны в предварительной версии
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
но не входят в релизный гейт v0.1. Полные границы описаны в документе
[объём v0.1](docs/v0.1-scope.md).

<a id="quick-start"></a>

## Быстрый старт

Вам понадобятся Docker с Compose и OpenSSL. В клоне этого репозитория сгенерируйте
локальные секреты и запустите стек:

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

Сохраните эти значения: от них зависит том Compose, а повторная генерация не меняет
существующий пароль базы данных и ключи защиты.

Откройте `http://127.0.0.1:8080/dashboard/` и войдите с начальным (bootstrap) ключом
администратора. Создайте постоянный ключ администратора, затем удалите
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` и пересоздайте контейнер API. Всё остальное описано
в [руководстве по эксплуатации](docs/operations.md): ротация ключей, резервное
копирование, обновления и базовые требования безопасности для продакшена.

<a id="deploy"></a>

## Развёртывание

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

На каждой платформе работает один и тот же образ контейнера: PostgreSQL, шаг миграции,
API и воркер, который должен работать постоянно. Перед созданием ресурсов сравните
требования к аккаунту, ограничения бесплатных тарифов и стоимость полного стека
в [матрице платформ развёртывания](deploy/README.md).

| Платформа | Конфигурация | Руководство |
| --- | --- | --- |
| **Одна VM** (Oracle, Google Cloud, Hetzner, DigitalOcean, ваш собственный сервер) | Самый дешёвый вариант для постоянной работы: весь стек на одной Linux-машине за Cloudflare Tunnel, без открытых входящих портов. От USD 0 в месяц на Oracle Cloud Always Free. | [Руководство по одной VM](deploy/vps/README.md) |
| Render | Бесплатный компактный сервис и временная база PostgreSQL для оценки; платные компактные или раздельные Blueprints | [Render](deploy/render/README.md) |
| Railway | PostgreSQL и один сервис с API и воркером; обычно укладывается в тариф Hobby за USD 5 | [Railway](deploy/railway/README.md) |
| Heroku | API, воркер и управляемый PostgreSQL на двух dyno Basic; бесплатного тарифа нет | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, воркер и задача миграции из App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Группы процессов с приватным Managed Postgres | [Fly.io](deploy/fly/README.md) |
| Koyeb | Управляемая база данных, HTTPS API и приватный воркер через CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | Полный стек из определения Compose в репозитории на вашем сервере | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Обычные манифесты для API, воркера и задачи миграции; PostgreSQL предоставляете вы | [Kubernetes](deploy/k8s/README.md) |

Платформы с масштабированием до нуля и serverless-платформы подходят плохо: воркер
принимает уведомления магазинов, доставляет вебхуки и каждую минуту выполняет сверку.

<a id="sdks"></a>

## SDK

Мобильные приложения обращаются к IAPStack через краткосрочные сессии клиентов.
Постоянный ключ приложения хранится на вашем бэкенде и никогда не должен попадать
в мобильный бинарный файл или браузерный бандл.

| Платформа | Пакет | Статус |
| --- | --- | --- |
| Flutter | [Нейтральный к провайдеру клиент](sdk/flutter/iapstack/README.md) с компаньонами для [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) и [Huawei](sdk/flutter/iapstack_huawei/README.md) | Входит в объём релиза v0.1 |
| iOS (Swift) | [HTTP-клиент и компаньон StoreKit 2](sdk/ios/README.md) | Реализован, ещё не опубликован |
| Android (Kotlin) | [HTTP-клиент с адаптерами Play Billing и HMS](sdk/android/README.md) | Реализован, ещё не опубликован |
| React Native | [HTTP-клиент и нативный мост StoreKit/Play/Huawei](sdk/react-native/README.md) | Реализован, ещё не опубликован |
| Go (бэкенд) | [Сессии клиентов, запрос прав доступа, проверка вебхуков](sdk/go/README.md) | Реализован, ещё не опубликован |
| TypeScript (бэкенд) | [Сессии клиентов, запрос прав доступа, проверка вебхуков](sdk/typescript/README.md) | Реализован, ещё не опубликован |

Публикация в реестрах отслеживается в [#141](https://github.com/imariman/iapstack/issues/141).
[Руководство по релизам SDK](docs/sdk-releases.md) описывает установку из исходников,
версионирование и автоматизацию релизов.

<a id="documentation"></a>

## Документация

Документация написана на английском языке.

| Руководство | Что описывает |
| --- | --- |
| [Эксплуатация](docs/operations.md) | Секреты, конфигурация, панель управления, настройка уведомлений магазинов, резервное копирование, обновления, релизы |
| [HTTP API v1](docs/api-v1.md) | Эндпоинты, учётные данные провайдеров, уведомления, проверка вебхуков |
| [Объём и архитектура v0.1](docs/v0.1-scope.md) | Системный контекст, ключевые инварианты, срезы по провайдерам |
| [Безопасность](docs/security.md) | Модель угроз и чек-лист безопасности релиза |
| [Архитектурные решения](docs/adr/README.md) | Почему существуют основные границы |
| [Платформы развёртывания](deploy/README.md) | Сравнение провайдеров и руководства по каждому из них |
| [Регламент релиза](docs/releases/v0.1.0-rc.1.md) | Релизный гейт с реальными магазинами для трёх провайдеров |

<a id="development"></a>

## Разработка

Используйте инструментарий Go, указанный в `go.mod`. Запустите PostgreSQL, примените
миграции и запустите API и воркер в одном процессе:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

Серверу также нужны ключи защиты из [быстрого старта](#quick-start); все настройки
перечислены в [справочнике по конфигурации](docs/operations.md#configuration-reference).
API слушает `:8080`, отдаёт `/healthz` и `/readyz` и встраивает панель управления
по адресу `/dashboard/`.

Запустите серверные проверки и сквозной релизный гейт:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

В [руководстве для участников](CONTRIBUTING.md) описаны проверки SDK, настройка
интеграционных тестов, обзор архитектуры и соглашения о коде.

<a id="repository-layout"></a>

### Структура репозитория

```text
.
├── cmd/iapstack/       # Точки входа API, воркера и миграций
├── internal/
│   ├── core/           # Клиенты, транзакции, продукты и права доступа
│   ├── verification/   # Нейтральная к провайдеру проверка покупок
│   ├── stores/         # Адаптеры Apple, Google Play и Huawei
│   ├── credentials/    # Защищённые учётные данные приложений
│   ├── protection/     # Порт защиты конфиденциальных данных
│   ├── persistence/    # Репозитории PostgreSQL
│   └── platform/       # Реализации конфигурации, логирования и хранилища ключей
├── dashboard/          # Встроенная операционная панель
├── contracts/          # Контракты OpenAPI и вебхуков
├── sdk/                # SDK для Flutter, iOS, Android, React Native, Go и TypeScript
├── deploy/             # Определения для Compose, облаков, VM и Kubernetes
└── docs/               # Руководства, ADR и регламенты релизов
```

<a id="roadmap"></a>

## Дорожная карта

- [x] Не зависящая от магазина доменная модель, хранение в PostgreSQL и версионированный контракт OpenAPI
- [x] Адаптеры Apple App Store, Google Play и Huawei AppGallery
- [x] Сверка жизненного цикла и подписанные исходящие вебхуки
- [x] Операционная панель и определения развёртывания для облаков, VM и Kubernetes
- [x] SDK для Flutter, Swift, Kotlin, React Native, Go и TypeScript
- [x] Миграция клиентов, импорт из RevenueCat, предложения (offerings) и финансовые отчёты Apple (`v0.2.0-alpha.1`)
- [ ] Пройти гейты с реальными магазинами для трёх провайдеров и опубликовать `v0.1.0-rc.1`
- [ ] Опубликовать стабильную `v0.1.0`
- [ ] Опубликовать версионированные пакеты SDK ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Признание выручки по финансовым отчётам Google Play и Huawei
- [ ] Amazon Appstore и адаптеры других магазинов

Отмеченные пункты реализованы в репозитории; это не означает готовности к продакшену.

<a id="contributing"></a>

## Участие в разработке

Мы рады сообщениям об ошибках, исправлениям и экспертизе по магазинам. Перед открытием
pull request прочитайте [руководство для участников](CONTRIBUTING.md), с вопросами
обращайтесь к [руководству по поддержке](SUPPORT.md), а об уязвимостях сообщайте
согласно [политике безопасности](SECURITY.md). Участие в проекте регулируется
[Кодексом поведения](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Лицензия

[Apache License 2.0](LICENSE)
