<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Маскот IAPStack — гофер" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Self-hosted проверка встроенных покупок и управление правами доступа с открытым исходным кодом.</strong>
  <br>
  Единая модель на стороне бэкенда для Apple App Store, Google Play и Huawei AppGallery.
  <br>
  Написан на Go. Без привязки к магазину. Разворачивается где угодно.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/ru/"><strong>Сайт</strong></a>
  ·
  <a href="#quick-start"><strong>Быстрый старт</strong></a>
  ·
  <a href="#deploy"><strong>Развёртывание</strong></a>
  ·
  <a href="#documentation"><strong>Документация</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Участие</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="Операционная панель IAPStack: состояние маршрута покупок, активность проверок с выделенным при наведении днём, признанная выручка из магазинов и объём тестовых покупок в песочнице для демопроекта" width="100%">
  </picture>
</p>

<p align="center">
  <sub>Операционная панель следует системной теме (в продукте: Системная / Светлая / Тёмная). Показаны синтетические демоданные.</sub>
</p>

> [!NOTE]
> Это перевод [README на английском языке](README.md). При расхождениях приоритет имеет английская версия. Разделы о разработке, структуре репозитория и пошаговом развёртывании ведутся только на английском.

> [!IMPORTANT]
> IAPStack — предрелизное программное обеспечение. Существующие релизы `v0.1.0-rc1`
> и `v0.1.0-rc2` — устаревшие предварительные версии, выпущенные до текущего
> релизного гейта и исправлений безопасности; они не поддерживаются для использования
> в продакшене. Основная ветка остаётся в разработке, и её гейт проверки жизненного
> цикла с реальными магазинами для всех трёх провайдеров ещё не пройден.

IAPStack — это self-hosted бэкенд и панель управления для проверки встроенных покупок
и превращения транзакций магазинов в надёжно хранимые права доступа приложения. Он даёт
командам единую модель на стороне бэкенда для всех мобильных магазинов, а учётные данные,
данные о покупках и решения о доступе остаются на инфраструктуре под их контролем.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Одна проверка для всех магазинов | Права доступа, а не чеки | Под вашим управлением |
| --- | --- | --- |
| Серверная проверка для Apple App Store, Google Play и Huawei AppGallery за единой моделью, не зависящей от магазина. | Приложения запрашивают, к чему у клиента есть доступ, вместо того чтобы разбирать сырые чеки и состояния магазинов. | Self-hosted по умолчанию: инфраструктура, учётные данные, данные о покупках и решения о доступе принадлежат вам. |

<a id="current-scope"></a>

## Текущий объём

- Серверная проверка подписок и непотребляемых бессрочных покупок
  из Apple App Store, Google Play и Huawei AppGallery
- Модель транзакций и прав доступа, не зависящая от магазина, на базе PostgreSQL
- Надёжный приём уведомлений, сверка жизненного цикла и подписанные исходящие вебхуки
- Операционная панель с аутентификацией (темы «Системная» / «Светлая» / «Тёмная»,
  сворачиваемая панель проектов и интерактивные графики активности и подписок)
  с версионированными контрактами HTTP/OpenAPI
- Нейтральные к провайдеру и специфичные для магазинов пакеты Flutter для всех трёх текущих провайдеров
- Компактное развёртывание или раздельные API и воркер с Docker и документированными облачными платформами

Выдача потребляемых покупок, миграция клиентов и объединение псевдонимов, работа в виде
хостинга и магазины помимо трёх перечисленных выходят за рамки v0.1. Полные границы
описаны в документе [объём v0.1](docs/v0.1-scope.md).

<a id="sdk-roadmap"></a>

## Дорожная карта SDK

Flutter остаётся единственным мобильным клиентом в объёме релиза v0.1: нейтральный
к провайдеру HTTP-клиент плюс компаньоны для Apple, Google Play и Huawei. Код
дополнительных SDK уже находится в репозитории в соответствии с планом расширения в
[#85](https://github.com/imariman/iapstack/issues/85). Его наличие не расширяет релизный
гейт v0.1 и не делает эти пакеты продакшен-релизами.

| SDK | Реализовано в репозитории | Оставшаяся работа |
| --- | --- | --- |
| [Go для доверенного хоста](sdk/go/README.md) | Сессии клиентов, серверный запрос прав доступа и обработка подписанных вебхуков | Первая публикация версионированного модуля ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript для доверенного хоста](sdk/typescript/README.md) | Сессии клиентов, запрос прав доступа, подписанные вебхуки и устанавливаемые архивы ESM/типов | Первая публикация в npm ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | HTTP-клиент, компаньон StoreKit 2, запускаемый пример с конфигурацией StoreKit, корневой манифест SwiftPM | Первый тег версии SDK ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | HTTP-клиент, адаптеры Play Billing/HMS, пример для доверенного хоста и подготовленные Maven-бинарники и jar-файлы с исходниками | Первая публикация в GitHub Maven ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | HTTP-клиент, нативный мост StoreKit/Play/Huawei, нативное примерное приложение и упакованные исходники компаньонов | Первая публикация в npm ([#141](https://github.com/imariman/iapstack/issues/141)) |

[Руководство по релизам SDK](docs/sdk-releases.md) описывает установку, независимые
предрелизные версии, проверки пакетов и настройку реестров. Наличие кода в этом
репозитории не означает, что в реестре уже есть релиз.

Долгоживущий bearer-токен приложения хранится на бэкенде доверенного хоста и никогда
не должен попадать в мобильный бинарный файл или браузерный бандл. Мобильные клиенты
используют краткосрочные сессии клиентов.

<a id="quick-start"></a>

## Быстрый старт

Вам понадобятся Docker с Compose и OpenSSL. В клоне этого репозитория сгенерируйте
секреты только для локального использования и запустите полный стек:

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

Надёжно сохраните сгенерированные значения и используйте их повторно с этим томом
Compose. Повторная генерация не меняет существующий пароль базы данных и ключи защиты.

Затем откройте `http://127.0.0.1:8080/dashboard/` и подключитесь с начальным
(bootstrap) ключом администратора. Создайте и сохраните постоянный ключ администратора,
удалите `IAPSTACK_BOOTSTRAP_ADMIN_KEY` из окружения и пересоздайте контейнер API.
[Руководство по эксплуатации](docs/operations.md) описывает начальную настройку, ротацию
ключей, остановку, резервное копирование, обновления и базовые требования безопасности
для продакшена.

<a id="deploy"></a>

## Развёртывание

Перед созданием ресурсов у провайдера сравните требования к аккаунту, ограничения
бесплатных тарифов и стоимость полного стека в
[матрице платформ развёртывания](deploy/README.md).

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Платформа | Конфигурация | Руководство |
| --- | --- | --- |
| Render | Один бесплатный веб-сервис с API и воркером, бесплатная база PostgreSQL для тестирования и платные компактные или раздельные Blueprints | [Руководство Render](deploy/render/README.md) |
| Heroku | API, воркер и управляемый PostgreSQL на двух постоянно работающих dyno Basic; бесплатного рантайма нет | [Руководство Heroku](deploy/heroku/README.md) |
| DigitalOcean | App Platform с воркером и задачей миграции или более дешёвый Droplet | [Руководство App Platform](deploy/digitalocean/README.md) · [Руководство Droplet](deploy/digitalocean/droplet.md) |
| Fly.io | Группы процессов с приватным Managed Postgres; только короткий бесплатный пробный период | [Руководство Fly.io](deploy/fly/README.md) |
| Koyeb | Управляемая база данных, HTTPS API и приватный воркер через официальный CLI | [Руководство Koyeb](deploy/koyeb/README.md) |
| Railway | PostgreSQL и один сервис с API и воркером; около USD 5 в месяц на тарифе Hobby | [Руководство Railway](deploy/railway/README.md) |
| Coolify | Полный стек из определения Docker Compose в репозитории на вашем собственном сервере | [Руководство Coolify](deploy/coolify/README.md) |
| Одна VM | Самый дешёвый вариант для постоянной работы: PostgreSQL, миграции, API и воркер на одной Linux-машине за Cloudflare Tunnel | [Руководство по одной VM](deploy/vps/README.md) |

### Самый недорогой вариант: одна VM

Одна Linux-машина запускает всё из
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml): PostgreSQL в приватной сети
Docker, одноразовый контейнер миграций, один компактный процесс с API и надёжным
воркером, а также коннектор Cloudflare Tunnel, поэтому VM не нужны открытые входящие
порты. Воркеру нужен постоянно работающий процесс, поэтому небольшая постоянно
включённая VM подходит лучше, чем платформы с масштабированием до нуля.

| Хост | Типичная стоимость | Особенности |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | До 4 Arm OCPU и 24 ГБ RAM; стек Resource Manager в один клик. |
| Google Cloud e2-micro (Free Tier, регионы США) | USD 0–4 в месяц | 1 ГБ RAM для небольшого трафика; пошаговое руководство в Cloud Shell. |
| Hetzner Cloud CAX11 / CX22 | Около EUR 4–5 в месяц | Предсказуемая оплата; установка из одного файла cloud-init. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | USD 6 / 12 в месяц | Публичный IPv4 включён; тот же файл cloud-init, что и для Hetzner. |
| Уже используемый вами сервер | USD 0 | Работает за NAT, но уведомлениям магазинов нужна непрерывная доступность. |

Также понадобится домен, DNS которого обслуживается на бесплатном тарифе Cloudflare.
Перед созданием ресурсов проверьте актуальные условия каждого провайдера. Шаги установки,
резервное копирование и обновления описаны в [разделе Deploy английского README](README.md#deploy)
и в [руководстве по одной VM](deploy/vps/README.md).

<a id="design-principles"></a>

## Принципы проектирования

**Self-hosted по умолчанию.** Инфраструктура, учётные данные, данные о покупках и решения о доступе принадлежат вам.

**Магазины — это адаптеры.** API конкретных магазинов изолированы за общими контрактами; ядро предметной области не зависит от какой-либо одной площадки.

**Права доступа — граница продукта.** Приложения запрашивают, к чему у клиента есть доступ, вместо того чтобы разбирать сырые чеки или состояния магазинов.

**Развёртывание где угодно.** Цель — простое развёртывание в контейнерах без зависимости от конкретной облачной платформы.

**Независимая реализация.** Поведение и требования безопасности определяются официальной документацией магазинов. Другие проекты с открытым исходным кодом могут изучаться ради совместимости и пограничных случаев, но код IAPStack реализован независимо.

<a id="documentation"></a>

## Документация

Документация написана на английском языке.

- [Объём и архитектура v0.1](docs/v0.1-scope.md)
- [Architecture Decision Records](docs/adr/README.md)
- [HTTP API v1 и контракты уведомлений провайдеров](docs/api-v1.md)
- [Машиночитаемый контракт OpenAPI v1](contracts/openapi/v1.yaml)
- [Руководство по эксплуатации и панели управления](docs/operations.md)
- [Модель угроз v0.1 и чек-лист безопасности релиза](docs/security.md)
- [Политика безопасности и сообщение об уязвимостях](SECURITY.md)
- [Руководство для участников](CONTRIBUTING.md)
- [Руководство по поддержке](SUPPORT.md)
- [Нейтральный к провайдеру Flutter SDK](sdk/flutter/iapstack/README.md)
- [Нативный iOS Swift SDK и компаньон StoreKit](sdk/ios/README.md)
- [Go SDK для доверенного хоста](sdk/go/README.md)
- [TypeScript SDK для доверенного хоста](sdk/typescript/README.md)

<a id="development"></a>

## Разработка

Сборка из исходников, конфигурация, тесты и структура репозитория описаны в
[разделе Development английского README](README.md#development). Перед открытием
pull request прочитайте [руководство для участников](CONTRIBUTING.md).

<a id="roadmap"></a>

## Дорожная карта

- [x] Определить ядро предметной области, контракты публичного API и модель хранения данных
- [x] Реализовать первые адаптеры для Apple App Store, Google Play и Huawei AppGallery
- [x] Добавить миграции PostgreSQL и Docker-конфигурацию, приближенную к продакшену
- [x] Создать нейтральный к провайдеру Flutter SDK, три Flutter SDK для конкретных провайдеров и панель управления
- [x] Поддержать сверку жизненного цикла и подписанные исходящие вебхуки
- [x] Опубликовать машиночитаемый контракт API v1 с проверками совместимости клиентов
- [x] Реализовать SDK для Go, TypeScript, Swift, Kotlin и React Native
- [x] Поддержать миграцию клиентов, объединение псевдонимов, импорт из RevenueCat и импорт финансовых отчётов
- [x] Добавить манифесты развёртывания для Kubernetes
- [ ] Пройти релизные гейты жизненного цикла для Apple App Store, Google Play и Huawei и опубликовать `v0.1.0-rc.1`
- [ ] Учесть отзывы о релиз-кандидате и опубликовать стабильную `v0.1.0`
- [ ] Опубликовать версионированные пакеты для хоста, Android, iOS и React Native ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Добавить Amazon Appstore и адаптеры других магазинов

Выполненные пункты описывают реализацию в репозитории, а не готовность к продакшен-релизу.

<a id="license"></a>

## Лицензия

Распространяется по лицензии [Apache License 2.0](LICENSE).

Участвуя в этом проекте, вы соглашаетесь соблюдать
[Кодекс поведения](CODE_OF_CONDUCT.md). О том, как внести вклад и получить поддержку,
читайте в [CONTRIBUTING.md](CONTRIBUTING.md) и [SUPPORT.md](SUPPORT.md).
