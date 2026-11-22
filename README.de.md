<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack-Gopher-Maskottchen" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verifizierung von In-App-Käufen und Berechtigungen zum Selbsthosten.</strong>
  <br>
  Ein Modell in deinem Backend für Apple App Store, Google Play und Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/de/">Website</a>
  ·
  <a href="#quick-start">Schnellstart</a>
  ·
  <a href="#deploy">Deployment</a>
  ·
  <a href="#documentation">Dokumentation</a>
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
    · <strong>Deutsch</strong>
    · <a href="README.es.md">Español</a>
    · <a href="README.fr.md">Français</a>
    · <a href="README.it.md">Italiano</a>
    · <a href="README.pt-BR.md">Português (Brasil)</a>
    · <a href="README.tr.md">Türkçe</a>
    · <a href="README.ru.md">Русский</a>
    · <a href="README.ja.md">日本語</a>
    · <a href="README.ko.md">한국어</a>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack-Betriebs-Dashboard: Zustand der Kaufroute, Verifizierungsaktivität mit einem hervorgehobenen Tag, erfasste Store-Umsätze und Sandbox-Testwert für ein Demoprojekt" width="100%">
  </picture>
</p>

> [!NOTE]
> Dies ist eine Übersetzung der [englischen README](README.md). Bei Abweichungen ist die englische Fassung maßgeblich.

IAPStack ist ein Backend mit Control Panel, das In-App-Käufe serverseitig verifiziert
und Store-Transaktionen in dauerhafte Berechtigungen umwandelt. Deine Apps stellen eine
einzige Frage, *worauf darf dieser Kunde zugreifen?*, und die Antwort ist dieselbe, egal
aus welchem Store der Kauf stammt. Zugangsdaten, Kaufdaten und Zugriffsentscheidungen
bleiben auf Infrastruktur, die du selbst betreibst.

| Einmal prüfen, überall | Berechtigungen statt Belege | Du betreibst es |
| --- | --- | --- |
| Serverseitige Verifizierung für drei Stores hinter einem store-neutralen Modell. Store-APIs bleiben in Adaptern isoliert. | Apps lesen den Zugriffsstatus, statt rohe Belege und store-spezifische Zustände auszuwerten. | Ein einzelnes Go-Binary und PostgreSQL. Läuft in Docker bei jedem Cloud-Anbieter oder auf einer VM, die du bereits hast. |

> [!IMPORTANT]
> IAPStack ist eine Vorabversion und wird für den Produktiveinsatz noch nicht
> unterstützt. Die Releases `v0.1.0-rc1` und `v0.1.0-rc2` sind ältere Vorschauversionen;
> der erste unterstützte Release Candidate erscheint, sobald die Lebenszyklus-Gates mit
> echten Stores für alle drei Anbieter bestanden sind.

<a id="features"></a>

## Funktionen

- **Verifizierung** von Abonnements und nicht verbrauchbaren Käufen auf Lebenszeit aus
  Apple App Store, Google Play und Huawei AppGallery
- **Lebenszyklus-Verfolgung** durch dauerhafte Annahme von Store-Benachrichtigungen und
  regelmäßigen Abgleich
- **Signierte ausgehende Webhooks**, damit dein Backend von jeder Änderung einer
  Berechtigung erfährt
- **Betriebs-Dashboard** für Anwendungen, Kunden, Warteschlangen und Webhook-Zustellungen,
  in hellem und dunklem Design
- **Versionierte HTTP-API** mit einem maschinenlesbaren [OpenAPI-Vertrag](contracts/openapi/v1.yaml)
- **Client-SDKs** für Flutter, iOS, Android und React Native sowie Go- und
  TypeScript-SDKs für dein eigenes Backend
- **Flexible Topologie**: ein kompakter Prozess oder getrennte API- und Worker-Dienste

Erfüllung verbrauchbarer Käufe, gehosteter Betrieb sowie Stores über diese drei hinaus
liegen außerhalb des v0.1-Umfangs. Kundenmigration, RevenueCat-Importe, Offerings und
Apple-Finanzberichte existieren bereits auf `main` und sind als Vorschau in
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1)
enthalten, gehören aber nicht zum Release-Gate von v0.1. Die vollständige Abgrenzung
findest du im [v0.1-Umfang](docs/v0.1-scope.md).

<a id="quick-start"></a>

## Schnellstart

Du brauchst Docker mit Compose und OpenSSL. Erzeuge in einem Klon dieses Repositorys
lokale Secrets und starte den Stack:

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

Bewahre diese Werte auf: Das Compose-Volume hängt von ihnen ab, und neu erzeugte Werte
rotieren weder das bestehende Datenbankpasswort noch die Schutzschlüssel.

Öffne `http://127.0.0.1:8080/dashboard/`, wähle „Use an admin or bootstrap key
instead“ und melde dich mit dem Bootstrap-Administratorschlüssel an. Erstelle im
Einrichtungsbanner dein Administratorkonto; ab dann meldest du dich mit diesem
Benutzernamen und Passwort an. Entferne anschließend `IAPSTACK_BOOTSTRAP_ADMIN_KEY`
und erstelle den API-Container neu. Der
[Betriebsleitfaden](docs/operations.md) erklärt den Rest: Schlüsselrotation, Backups,
Upgrades und die Sicherheitsgrundlage für den Produktivbetrieb.

<a id="deploy"></a>

## Deployment

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Jedes Ziel betreibt dasselbe Container-Image mit PostgreSQL, einem Migrationsschritt, der
API und einem Worker, der dauerhaft laufen muss. Vergleiche Kontoanforderungen,
Einschränkungen der kostenlosen Tarife und die Kosten des kompletten Stacks in der
[Übersicht der Deployment-Ziele](deploy/README.md), bevor du Ressourcen anlegst.

| Ziel | Aufbau | Leitfaden |
| --- | --- | --- |
| **Einzelne VM** (Oracle, Google Cloud, Hetzner, DigitalOcean, dein eigener Server) | Die günstigste Option für Dauerbetrieb: der komplette Stack auf einer Linux-Maschine hinter einem Cloudflare Tunnel, ohne offene eingehende Ports. Ab USD 0 pro Monat mit Oracle Cloud Always Free. | [Single-VM-Leitfaden](deploy/vps/README.md) |
| Render | Kostenloser kompakter Dienst und kurzlebiges PostgreSQL zum Testen; kostenpflichtige kompakte oder getrennte Blueprints | [Render](deploy/render/README.md) |
| Railway | PostgreSQL und ein Dienst mit API und Worker; meist innerhalb des Hobby-Tarifs für USD 5 | [Railway](deploy/railway/README.md) |
| Heroku | API, Worker und verwaltetes PostgreSQL auf zwei Basic-Dynos; kein kostenloser Tarif | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, Worker und Migrationsjob aus einer App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Prozessgruppen mit privatem Managed Postgres | [Fly.io](deploy/fly/README.md) |
| Koyeb | Verwaltete Datenbank, HTTPS-API und privater Worker über die CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | Der komplette Stack aus der Compose-Definition des Repositorys auf deinem Server | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Einfache Manifeste für API, Worker und Migrationsjob; PostgreSQL bringst du selbst mit | [Kubernetes](deploy/k8s/README.md) |

Plattformen, die auf null skalieren, und Serverless-Plattformen passen schlecht: Der
Worker nimmt Store-Benachrichtigungen an, stellt Webhooks zu und führt jede Minute den
Abgleich aus.

<a id="sdks"></a>

## SDKs

Mobile Apps sprechen über kurzlebige Kundensitzungen mit IAPStack. Der dauerhafte
Anwendungsschlüssel bleibt in deinem Backend und darf niemals in einem mobilen Binary oder
Browser-Bundle ausgeliefert werden.

| Plattform | Paket | Status |
| --- | --- | --- |
| Flutter | [Anbieterneutraler Client](sdk/flutter/iapstack/README.md) mit Begleitpaketen für [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) und [Huawei](sdk/flutter/iapstack_huawei/README.md) | Release-Umfang von v0.1 |
| iOS (Swift) | [HTTP-Client und StoreKit-2-Begleiter](sdk/ios/README.md) | Implementiert, noch nicht veröffentlicht |
| Android (Kotlin) | [HTTP-Client mit Play-Billing- und HMS-Adaptern](sdk/android/README.md) | Implementiert, noch nicht veröffentlicht |
| React Native | [HTTP-Client und native StoreKit/Play/Huawei-Bridge](sdk/react-native/README.md) | Implementiert, noch nicht veröffentlicht |
| Go (Backend) | [Kundensitzungen, Abfrage von Berechtigungen, Webhook-Verifizierung](sdk/go/README.md) | Implementiert, noch nicht veröffentlicht |
| TypeScript (Backend) | [Kundensitzungen, Abfrage von Berechtigungen, Webhook-Verifizierung](sdk/typescript/README.md) | Implementiert, noch nicht veröffentlicht |

Die Veröffentlichung in Registries wird in [#141](https://github.com/imariman/iapstack/issues/141)
verfolgt. Der [SDK-Release-Leitfaden](docs/sdk-releases.md) beschreibt die Installation aus
dem Quellcode, die Versionierung und die Release-Automatisierung.

<a id="documentation"></a>

## Dokumentation

Die Dokumentation ist auf Englisch verfasst.

| Leitfaden | Inhalt |
| --- | --- |
| [Betrieb](docs/operations.md) | Secrets, Konfiguration, Dashboard, Einrichtung von Store-Benachrichtigungen, Backups, Upgrades, Releases |
| [HTTP-API v1](docs/api-v1.md) | Endpunkte, Anbieter-Zugangsdaten, Benachrichtigungen, Webhook-Verifizierung |
| [v0.1-Umfang und Architektur](docs/v0.1-scope.md) | Systemkontext, zentrale Invarianten, Anbieter-Slices |
| [Sicherheit](docs/security.md) | Bedrohungsmodell und Sicherheits-Checkliste für Releases |
| [Architekturentscheidungen](docs/adr/README.md) | Warum es die wichtigsten Grenzen gibt |
| [Deployment-Ziele](deploy/README.md) | Anbietervergleich und Leitfäden pro Anbieter |
| [Release-Runbook](docs/releases/v0.1.0-rc.1.md) | Das Release-Gate mit echten Stores für alle drei Anbieter |

<a id="development"></a>

## Entwicklung

Verwende die in `go.mod` angegebene Go-Toolchain. Starte PostgreSQL, wende die Migrationen
an und führe API und Worker in einem Prozess aus:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

Der Server braucht außerdem die Schutzschlüssel aus dem [Schnellstart](#quick-start); alle
Einstellungen stehen in der [Konfigurationsreferenz](docs/operations.md#configuration-reference).
Die API lauscht auf `:8080`, stellt `/healthz` und `/readyz` bereit und liefert das
eingebettete Dashboard unter `/dashboard/` aus.

Führe die Serverprüfungen und das End-to-End-Release-Gate aus:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

Der [Leitfaden zum Mitwirken](CONTRIBUTING.md) enthält die SDK-Prüfungen, die Einrichtung
der Integrationstests, einen Architekturüberblick und die Code-Konventionen.

<a id="repository-layout"></a>

### Repository-Struktur

```text
.
├── cmd/iapstack/       # Einstiegspunkte für API, Worker und Migrationen
├── internal/
│   ├── core/           # Kunden, Transaktionen, Produkte und Berechtigungen
│   ├── verification/   # Anbieterneutrale Kaufverifizierung
│   ├── stores/         # Adapter für Apple, Google Play und Huawei
│   ├── credentials/    # Geschützte Anwendungs-Zugangsdaten
│   ├── protection/     # Port zum Schutz sensibler Daten
│   ├── persistence/    # PostgreSQL-Repositorys
│   └── platform/       # Konfiguration, Logging und Keyring-Implementierungen
├── dashboard/          # Eingebettetes Betriebs-Dashboard
├── contracts/          # OpenAPI- und Webhook-Verträge
├── sdk/                # SDKs für Flutter, iOS, Android, React Native, Go und TypeScript
├── deploy/             # Compose-, Cloud-, VM- und Kubernetes-Definitionen
└── docs/               # Leitfäden, ADRs und Release-Runbooks
```

<a id="roadmap"></a>

## Roadmap

- [x] Store-neutrale Domäne, PostgreSQL-Persistenz und versionierter OpenAPI-Vertrag
- [x] Adapter für Apple App Store, Google Play und Huawei AppGallery
- [x] Abgleich über den Lebenszyklus und signierte ausgehende Webhooks
- [x] Betriebs-Dashboard und Deployment-Definitionen für Cloud, VM und Kubernetes
- [x] SDKs für Flutter, Swift, Kotlin, React Native, Go und TypeScript
- [x] Kundenmigration, RevenueCat-Importe, Offerings und Apple-Finanzberichte (`v0.2.0-alpha.1`)
- [x] Abonnementmetriken, Kundensupport-Werkzeuge und Antworten auf Apples Consumption-Anfragen bei Rückerstattungen (auf `main`, noch nicht veröffentlicht)
- [ ] Die Gates mit echten Stores für alle drei Anbieter bestehen und `v0.1.0-rc.1` veröffentlichen
- [ ] Das stabile `v0.1.0` veröffentlichen
- [ ] Versionierte SDK-Pakete veröffentlichen ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] MCP-Endpunkt, über den KI-Agenten IAPStack mit minimal berechtigten Schlüsseln abfragen und bedienen können ([#285](https://github.com/imariman/iapstack/issues/285))
- [ ] Erfassung von Finanzberichten für Google Play und Huawei
- [ ] Amazon Appstore und weitere Store-Adapter

Abgehakte Punkte sind im Repository implementiert; sie bedeuten keine Bereitschaft für den
Produktivbetrieb.

<a id="contributing"></a>

## Mitwirken

Fehlerberichte, Korrekturen und Store-Fachwissen sind willkommen. Lies den
[Leitfaden zum Mitwirken](CONTRIBUTING.md), bevor du einen Pull Request öffnest, den
[Support-Leitfaden](SUPPORT.md) bei Fragen und die [Sicherheitsrichtlinie](SECURITY.md), um
eine Schwachstelle zu melden. Für die Teilnahme gilt der
[Verhaltenskodex](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Lizenz

[Apache License 2.0](LICENSE)
