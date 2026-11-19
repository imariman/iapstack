<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack-Gopher-Maskottchen" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Open-Source-Verifizierung von In-App-Käufen und Berechtigungen zum Selbsthosten.</strong>
  <br>
  Ein Modell in deinem Backend für Apple App Store, Google Play und Huawei AppGallery.
  <br>
  In Go geschrieben. Store-unabhängig. Überall einsetzbar.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/de/"><strong>Website</strong></a>
  ·
  <a href="#quick-start"><strong>Schnellstart</strong></a>
  ·
  <a href="#deploy"><strong>Deployment</strong></a>
  ·
  <a href="#documentation"><strong>Dokumentation</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Mitwirken</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="IAPStack-Betriebs-Dashboard: Zustand der Kaufroute, Verifizierungsaktivität mit einem hervorgehobenen Tag, erfasste Store-Umsätze und Sandbox-Testwert für ein Demoprojekt" width="100%">
  </picture>
</p>

<p align="center">
  <sub>Das Betriebs-Dashboard folgt deinem Systemdesign (System / Hell / Dunkel im Produkt). Gezeigt mit synthetischen Demodaten.</sub>
</p>

> [!NOTE]
> Dies ist eine Übersetzung der [englischen README](README.md). Bei Abweichungen ist die englische Fassung maßgeblich. Details zu Entwicklung, Repository-Struktur und schrittweisem Deployment werden nur auf Englisch gepflegt.

> [!IMPORTANT]
> IAPStack ist eine Vorabversion. Die vorhandenen Releases `v0.1.0-rc1` und `v0.1.0-rc2`
> sind ältere Vorschauversionen, die vor dem aktuellen Release-Gate und den
> Sicherheitskorrekturen entstanden sind; sie werden für den Produktiveinsatz nicht
> unterstützt. Der Standard-Branch befindet sich weiter in Entwicklung, und sein
> Lebenszyklus-Gate mit echten Stores für alle drei Anbieter ist noch nicht abgeschlossen.

IAPStack ist ein selbst gehostetes Backend mit Control Panel, das In-App-Käufe validiert
und Store-Transaktionen in dauerhafte Berechtigungen für deine Anwendung umwandelt. Es
gibt Teams ein einziges Modell im eigenen Backend über alle mobilen Stores hinweg, während
Zugangsdaten, Kaufdaten und Zugriffsentscheidungen auf Infrastruktur bleiben, die sie
selbst kontrollieren.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Einmal prüfen, überall | Berechtigungen statt Belege | Du betreibst es |
| --- | --- | --- |
| Serverseitige Verifizierung für Apple App Store, Google Play und Huawei AppGallery hinter einem store-neutralen Modell. | Apps fragen, worauf ein Kunde zugreifen darf, statt rohe Belege und Store-Zustände auszuwerten. | Standardmäßig selbst gehostet: Infrastruktur, Zugangsdaten, Kaufdaten und Zugriffsentscheidungen gehören dir. |

<a id="current-scope"></a>

## Aktueller Umfang

- Serverseitige Verifizierung von Abonnements und nicht verbrauchbaren Käufen auf Lebenszeit
  aus Apple App Store, Google Play und Huawei AppGallery
- Ein store-neutrales Transaktions- und Berechtigungsmodell auf Basis von PostgreSQL
- Dauerhafte Annahme von Benachrichtigungen, Abgleich über den Lebenszyklus und signierte ausgehende Webhooks
- Ein authentifiziertes Betriebs-Dashboard (Designs System / Hell / Dunkel, einklappbare
  Projektleiste und untersuchbare Aktivitäts- und Abonnementdiagramme) mit versionierten
  HTTP/OpenAPI-Verträgen
- Anbieterneutrale und store-spezifische Flutter-Pakete für alle drei aktuellen Anbieter
- Kompaktes oder getrenntes API/Worker-Deployment mit Docker und dokumentierten Cloud-Zielen

Erfüllung verbrauchbarer Käufe, Kundenmigration und Alias-Zusammenführung, gehosteter
Betrieb sowie Stores über die drei genannten hinaus liegen außerhalb des v0.1-Umfangs.
Die vollständige Abgrenzung findest du im [v0.1-Umfang](docs/v0.1-scope.md).

<a id="sdk-roadmap"></a>

## SDK-Roadmap

Flutter bleibt der einzige mobile Client im Release-Umfang von v0.1: ein anbieterneutraler
HTTP-Client plus Begleitpakete für Apple, Google Play und Huawei. Weiterer SDK-Code liegt
bereits im Repository und folgt dem Erweiterungsplan in
[#85](https://github.com/imariman/iapstack/issues/85). Seine Existenz erweitert weder das
Release-Gate von v0.1, noch macht sie diese Pakete zu Produktiv-Releases.

| SDK | Im Repository implementiert | Offene Arbeit |
| --- | --- | --- |
| [Go Trusted Host](sdk/go/README.md) | Kundensitzungen, serverseitige Abfrage von Berechtigungen und Verarbeitung signierter Webhooks | Erste versionierte Modulveröffentlichung ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript Trusted Host](sdk/typescript/README.md) | Kundensitzungen, Abfrage von Berechtigungen, signierte Webhooks und installierbare ESM-/Typ-Archive | Erste npm-Veröffentlichung ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | HTTP-Client, StoreKit-2-Begleiter, lauffähiges Beispiel mit StoreKit-Konfiguration, SwiftPM-Manifest im Wurzelverzeichnis | Erster SDK-Versions-Tag ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | HTTP-Client, Play-Billing-/HMS-Adapter, Trusted-Host-Beispiel und bereitgestellte Maven-Binaries/Source-JARs | Erste Veröffentlichung auf GitHub Maven ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | HTTP-Client, native StoreKit/Play/Huawei-Bridge, native Beispiel-App und gepackte Begleitquellen | Erste npm-Veröffentlichung ([#141](https://github.com/imariman/iapstack/issues/141)) |

Der [SDK-Release-Leitfaden](docs/sdk-releases.md) beschreibt Installation, unabhängige
Vorabversionen, Paketprüfungen und Registry-Einrichtung. Code in diesem Repository bedeutet
nicht, dass bereits ein Release in einer Registry existiert.

Das dauerhafte Bearer-Token der Anwendung bleibt auf einem vertrauenswürdigen Host-Backend
und darf niemals in einem mobilen Binary oder Browser-Bundle ausgeliefert werden. Mobile
Clients verwenden kurzlebige Kundensitzungen.

<a id="quick-start"></a>

## Schnellstart

Du brauchst Docker mit Compose und OpenSSL. Erzeuge in einem Klon dieses Repositorys
rein lokale Secrets und starte den kompletten Stack:

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

Bewahre die erzeugten Werte sicher auf und verwende sie mit diesem Compose-Volume weiter.
Neu erzeugte Werte rotieren weder das bestehende Datenbankpasswort noch die Schutzschlüssel.

Öffne danach `http://127.0.0.1:8080/dashboard/` und verbinde dich mit dem
Bootstrap-Administratorschlüssel. Erstelle einen dauerhaften Administratorschlüssel und
bewahre ihn auf, entferne `IAPSTACK_BOOTSTRAP_ADMIN_KEY` aus der Umgebung und erstelle den
API-Container neu. Der [Betriebsleitfaden](docs/operations.md) erklärt Bootstrap,
Schlüsselrotation, Herunterfahren, Backups, Upgrades und die Sicherheitsgrundlage für den
Produktivbetrieb.

<a id="deploy"></a>

## Deployment

Vergleiche Kontoanforderungen, Einschränkungen der kostenlosen Tarife und die Kosten des
kompletten Stacks in der [Übersicht der Deployment-Ziele](deploy/README.md), bevor du
Ressourcen bei einem Anbieter anlegst.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Ziel | Aufbau | Leitfaden |
| --- | --- | --- |
| Render | Ein kostenloser Webdienst mit API und Worker, eine kostenlose PostgreSQL-Datenbank zum Testen sowie kostenpflichtige kompakte oder getrennte Blueprints | [Render-Leitfaden](deploy/render/README.md) |
| Heroku | API, Worker und verwaltetes PostgreSQL auf zwei dauerhaft laufenden Basic-Dynos; keine kostenlose Laufzeit | [Heroku-Leitfaden](deploy/heroku/README.md) |
| DigitalOcean | App Platform mit Worker und Migrationsjob oder ein günstigeres Droplet | [App-Platform-Leitfaden](deploy/digitalocean/README.md) · [Droplet-Leitfaden](deploy/digitalocean/droplet.md) |
| Fly.io | Prozessgruppen mit privatem Managed Postgres; nur kurze kostenlose Testphase | [Fly.io-Leitfaden](deploy/fly/README.md) |
| Koyeb | Verwaltete Datenbank, HTTPS-API und privater Worker über die offizielle CLI | [Koyeb-Leitfaden](deploy/koyeb/README.md) |
| Railway | PostgreSQL und ein Dienst mit API und Worker; etwa USD 5 pro Monat im Hobby-Tarif | [Railway-Leitfaden](deploy/railway/README.md) |
| Coolify | Der komplette Stack aus einer Docker-Compose-Definition im Repository auf deinem eigenen Server | [Coolify-Leitfaden](deploy/coolify/README.md) |
| Einzelne VM | Die günstigste Option für Dauerbetrieb: PostgreSQL, Migrationen, API und Worker auf einer Linux-Maschine hinter einem Cloudflare Tunnel | [Single-VM-Leitfaden](deploy/vps/README.md) |

### Günstigste Option: eine einzelne VM

Eine Linux-Maschine betreibt alles aus
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml): PostgreSQL in einem privaten
Docker-Netzwerk, einen einmalig laufenden Migrationscontainer, einen kompakten Prozess mit
API und dauerhaftem Worker sowie einen Cloudflare-Tunnel-Connector, sodass die VM keine
offenen eingehenden Ports braucht. Der Worker benötigt einen dauerhaft laufenden Prozess;
deshalb passt eine kleine, ständig laufende VM besser als Plattformen, die auf null skalieren.

| Host | Typische Kosten | Eignung |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | Bis zu 4 Arm-OCPUs und 24 GB RAM; Resource-Manager-Stack mit einem Klick. |
| Google Cloud e2-micro (Free Tier, US-Regionen) | USD 0–4/Monat | 1 GB RAM für wenig Traffic; geführte Anleitung in Cloud Shell. |
| Hetzner Cloud CAX11 / CX22 | Etwa EUR 4–5/Monat | Planbare Abrechnung; Installation aus einer einzigen cloud-init-Datei. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | USD 6 / 12 pro Monat | Öffentliche IPv4 inklusive; dieselbe cloud-init-Datei wie bei Hetzner. |
| Ein Server, den du bereits betreibst | USD 0 | Funktioniert hinter NAT, aber Store-Benachrichtigungen erfordern durchgehende Verfügbarkeit. |

Außerdem brauchst du eine Domain, deren DNS im kostenlosen Tarif von Cloudflare liegt.
Prüfe die aktuellen Bedingungen jedes Anbieters, bevor du Ressourcen anlegst. Die
Installationsschritte, Backups und Upgrades sind im
[Deploy-Abschnitt der englischen README](README.md#deploy) und im
[Single-VM-Leitfaden](deploy/vps/README.md) beschrieben.

<a id="design-principles"></a>

## Designprinzipien

**Standardmäßig selbst gehostet.** Infrastruktur, Zugangsdaten, Kaufdaten und Zugriffsentscheidungen gehören dir.

**Stores sind Adapter.** Store-spezifische APIs bleiben hinter gemeinsamen Verträgen isoliert; die Kerndomäne hängt von keinem einzelnen Marktplatz ab.

**Berechtigungen sind die Produktgrenze.** Anwendungen fragen, worauf ein Kunde zugreifen darf, statt rohe Belege oder Store-Zustände auszuwerten.

**Überall einsetzbar.** Ziel ist ein unkompliziertes Container-Deployment statt einer Abhängigkeit von einer bestimmten Cloud-Plattform.

**Unabhängige Implementierung.** Die offizielle Store-Dokumentation definiert Verhalten und Sicherheitsanforderungen. Andere Open-Source-Projekte können für Interoperabilität und Grenzfälle herangezogen werden, der Code von IAPStack wird jedoch unabhängig implementiert.

<a id="documentation"></a>

## Dokumentation

Die Dokumentation ist auf Englisch verfasst.

- [v0.1-Umfang und Architektur](docs/v0.1-scope.md)
- [Architecture Decision Records](docs/adr/README.md)
- [HTTP-API v1 und Verträge für Anbieter-Benachrichtigungen](docs/api-v1.md)
- [Maschinenlesbarer OpenAPI-v1-Vertrag](contracts/openapi/v1.yaml)
- [Leitfaden zu Betrieb und Dashboard](docs/operations.md)
- [v0.1-Bedrohungsmodell und Sicherheits-Checkliste für Releases](docs/security.md)
- [Sicherheitsrichtlinie und Melden von Schwachstellen](SECURITY.md)
- [Leitfaden zum Mitwirken](CONTRIBUTING.md)
- [Support-Leitfaden](SUPPORT.md)
- [Anbieterneutrales Flutter-SDK](sdk/flutter/iapstack/README.md)
- [Natives iOS-Swift-SDK und StoreKit-Begleiter](sdk/ios/README.md)
- [Go-SDK für vertrauenswürdige Hosts](sdk/go/README.md)
- [TypeScript-SDK für vertrauenswürdige Hosts](sdk/typescript/README.md)

<a id="development"></a>

## Entwicklung

Bauen aus dem Quellcode, Konfiguration, Tests und die Repository-Struktur sind im
[Development-Abschnitt der englischen README](README.md#development) beschrieben. Lies den
[Leitfaden zum Mitwirken](CONTRIBUTING.md), bevor du einen Pull Request öffnest.

<a id="roadmap"></a>

## Roadmap

- [x] Kerndomäne, öffentliche API-Verträge und Persistenzmodell definieren
- [x] Erste Adapter für Apple App Store, Google Play und Huawei AppGallery implementieren
- [x] PostgreSQL-Migrationen und ein produktionsnahes Docker-Setup hinzufügen
- [x] Das anbieterneutrale und drei anbieterspezifische Flutter-SDKs sowie das Dashboard bauen
- [x] Abgleich über den Lebenszyklus und signierte ausgehende Webhooks unterstützen
- [x] Einen maschinenlesbaren v1-API-Vertrag mit Kompatibilitätsprüfungen für Clients veröffentlichen
- [x] SDKs für Go, TypeScript, Swift, Kotlin und React Native implementieren
- [x] Kundenmigration, Alias-Zusammenführung, RevenueCat-Importe und Importe von Finanzberichten unterstützen
- [x] Kubernetes-Deployment-Manifeste hinzufügen
- [ ] Lebenszyklus-Release-Gates für Apple App Store, Google Play und Huawei abschließen und `v0.1.0-rc.1` veröffentlichen
- [ ] Feedback zum Release Candidate umsetzen und das stabile `v0.1.0` veröffentlichen
- [ ] Versionierte Host-, Android-, iOS- und React-Native-Pakete veröffentlichen ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Amazon Appstore und weitere Store-Adapter hinzufügen

Abgehakte Punkte beschreiben Implementierungen im Repository, nicht die Bereitschaft für ein Produktiv-Release.

<a id="license"></a>

## Lizenz

Lizenziert unter der [Apache License 2.0](LICENSE).

Mit deiner Teilnahme an diesem Projekt erklärst du dich bereit, den
[Verhaltenskodex](CODE_OF_CONDUCT.md) einzuhalten. Wege für Beiträge und Support findest
du in [CONTRIBUTING.md](CONTRIBUTING.md) und [SUPPORT.md](SUPPORT.md).
