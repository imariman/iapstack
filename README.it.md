<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascotte Gopher di IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verifica degli acquisti in-app ed entitlement, self-hosted.</strong>
  <br>
  Un unico modello gestito dal backend per Apple App Store, Google Play e Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/it/">Sito web</a>
  ·
  <a href="#quick-start">Avvio rapido</a>
  ·
  <a href="#deploy">Deployment</a>
  ·
  <a href="#documentation">Documentazione</a>
  ·
  <a href="contracts/openapi/v1.yaml">OpenAPI</a>
</p>

<p align="center">
  <a href="https://github.com/imariman/iapstack/actions/workflows/ci.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 License"></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8.svg" alt="Go 1.26">
  <img src="https://img.shields.io/badge/status-prerelease-orange.svg" alt="Prerelease">
</p>

<p align="center">
  <sub>
    <a href="README.md">English</a>
    · <a href="README.de.md">Deutsch</a>
    · <a href="README.es.md">Español</a>
    · <a href="README.fr.md">Français</a>
    · <strong>Italiano</strong>
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
    <img src="assets/dashboard-light.png" alt="Dashboard operativa di IAPStack: stato del percorso di acquisto, attività di verifica con un giorno evidenziato al passaggio del mouse, ricavi riconosciuti dagli store e valore dei test in sandbox per un progetto demo" width="100%">
  </picture>
</p>

> [!NOTE]
> Questo documento è una traduzione del [README in inglese](README.md). In caso di differenze, fa fede la versione inglese.

IAPStack è un backend con pannello di controllo che verifica gli acquisti in-app lato
server e trasforma le transazioni degli store in entitlement durevoli. Le tue app pongono
una sola domanda, *a cosa può accedere questo cliente?*, e la risposta è la stessa
qualunque sia lo store da cui proviene l'acquisto. Credenziali, dati di acquisto e
decisioni di accesso restano su un'infrastruttura gestita da te.

| Verifica una volta, ovunque | Entitlement, non ricevute | Lo gestisci tu |
| --- | --- | --- |
| Verifica lato server per tre store dietro un unico modello indipendente dagli store. Le API degli store restano isolate negli adapter. | Le app leggono lo stato di accesso invece di interpretare ricevute grezze e stati specifici di ogni store. | Un unico binario Go e PostgreSQL. Gira in Docker su qualsiasi cloud o su una VM che hai già. |

> [!IMPORTANT]
> IAPStack è software in pre-release e non è ancora supportato per la produzione. Le
> release `v0.1.0-rc1` e `v0.1.0-rc2` sono anteprime legacy; la prima release candidate
> supportata uscirà quando i gate sul ciclo di vita con store reali saranno superati per
> tutti e tre i provider.

<a id="features"></a>

## Funzionalità

- **Verifica** di abbonamenti e acquisti a vita non consumabili da Apple
  App Store, Google Play e Huawei AppGallery
- **Monitoraggio del ciclo di vita** tramite acquisizione durevole delle notifiche degli
  store e riconciliazione pianificata
- **Webhook in uscita firmati**, così il tuo backend viene informato di ogni modifica
  agli entitlement
- **Dashboard operativa** per applicazioni, clienti, code e consegne dei webhook,
  con tema chiaro e scuro
- **API HTTP versionata** con un [contratto OpenAPI](contracts/openapi/v1.yaml) leggibile dalle macchine
- **SDK client** per Flutter, iOS, Android e React Native, oltre a SDK Go e TypeScript
  per il tuo backend
- **Topologia flessibile**: un unico processo compatto, oppure servizi API e worker separati

L'evasione degli acquisti consumabili, l'operatività in hosting e gli store oltre questi
tre sono fuori dall'ambito della v0.1. Migrazione dei clienti, importazioni da RevenueCat,
offering e report finanziari Apple esistono già su `main` e sono disponibili in anteprima in
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
ma non fanno parte del release gate della v0.1. Consulta l'[ambito della v0.1](docs/v0.1-scope.md)
per il perimetro completo.

<a id="quick-start"></a>

## Avvio rapido

Ti servono Docker con Compose e OpenSSL. Da un clone di questo repository, genera
segreti locali e avvia lo stack:

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

Conserva questi valori: il volume Compose dipende da essi, e rigenerarli non ruota la
password del database né le chiavi di protezione esistenti.

Apri `http://127.0.0.1:8080/dashboard/` e accedi con la chiave di bootstrap
dell'amministratore. Crea una chiave di amministratore durevole, poi rimuovi
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` e ricrea il container dell'API. La
[guida operativa](docs/operations.md) copre il resto: rotazione delle chiavi, backup,
aggiornamenti e la baseline di sicurezza per la produzione.

<a id="deploy"></a>

## Deployment

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Ogni target esegue la stessa immagine container con PostgreSQL, una fase di migrazione,
l'API e un worker che deve restare in esecuzione. Confronta requisiti dell'account, limiti
dei piani gratuiti e costi dello stack completo nella
[matrice dei target di deployment](deploy/README.md) prima di creare risorse.

| Target | Configurazione | Guida |
| --- | --- | --- |
| **VM singola** (Oracle, Google Cloud, Hetzner, DigitalOcean, il tuo server) | L'opzione continua più economica: lo stack completo su una macchina Linux dietro un Cloudflare Tunnel, senza porte in ingresso aperte. Da USD 0 al mese su Oracle Cloud Always Free. | [Guida VM singola](deploy/vps/README.md) |
| Render | Servizio compatto gratuito e PostgreSQL usa e getta per la valutazione; Blueprint a pagamento compatti o separati | [Render](deploy/render/README.md) |
| Railway | PostgreSQL e un unico servizio API/worker; di solito rientra nel piano Hobby da USD 5 | [Railway](deploy/railway/README.md) |
| Heroku | API, worker e PostgreSQL gestito su due dyno Basic; nessun piano gratuito | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, worker e job di migrazione da una App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Process group con Managed Postgres privato | [Fly.io](deploy/fly/README.md) |
| Koyeb | Database gestito, API HTTPS e worker privato tramite la CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | Lo stack completo dalla definizione Compose del repository, sul tuo server | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Manifest semplici per API, worker e job di migrazione; PostgreSQL a carico tuo | [Kubernetes](deploy/k8s/README.md) |

Le piattaforme scale-to-zero e serverless non sono adatte: il worker acquisisce le
notifiche degli store, consegna i webhook ed esegue la riconciliazione ogni minuto.

<a id="sdks"></a>

## SDK

Le app mobili comunicano con IAPStack tramite sessioni cliente di breve durata. La chiave
durevole dell'applicazione resta sul tuo backend e non deve mai essere inclusa in un
binario mobile o in un bundle per browser.

| Piattaforma | Pacchetto | Stato |
| --- | --- | --- |
| Flutter | [Client neutrale rispetto al provider](sdk/flutter/iapstack/README.md) con companion per [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) e [Huawei](sdk/flutter/iapstack_huawei/README.md) | Ambito della release v0.1 |
| iOS (Swift) | [Client HTTP e companion StoreKit 2](sdk/ios/README.md) | Implementato, non ancora pubblicato |
| Android (Kotlin) | [Client HTTP con adapter Play Billing e HMS](sdk/android/README.md) | Implementato, non ancora pubblicato |
| React Native | [Client HTTP e bridge nativo StoreKit/Play/Huawei](sdk/react-native/README.md) | Implementato, non ancora pubblicato |
| Go (backend) | [Sessioni cliente, consultazione degli entitlement, verifica dei webhook](sdk/go/README.md) | Implementato, non ancora pubblicato |
| TypeScript (backend) | [Sessioni cliente, consultazione degli entitlement, verifica dei webhook](sdk/typescript/README.md) | Implementato, non ancora pubblicato |

La pubblicazione sui registry è tracciata in [#141](https://github.com/imariman/iapstack/issues/141).
La [guida alle release degli SDK](docs/sdk-releases.md) copre l'installazione dai sorgenti,
il versionamento e l'automazione delle release.

<a id="documentation"></a>

## Documentazione

La documentazione è scritta in inglese.

| Guida | Contenuto |
| --- | --- |
| [Operatività](docs/operations.md) | Segreti, configurazione, dashboard, configurazione delle notifiche degli store, backup, aggiornamenti, release |
| [API HTTP v1](docs/api-v1.md) | Endpoint, credenziali dei provider, notifiche, verifica dei webhook |
| [Ambito e architettura della v0.1](docs/v0.1-scope.md) | Contesto del sistema, invarianti fondamentali, slice per provider |
| [Sicurezza](docs/security.md) | Threat model e checklist di sicurezza per le release |
| [Decisioni architetturali](docs/adr/README.md) | Perché esistono i confini principali |
| [Target di deployment](deploy/README.md) | Confronto tra provider e guide per ciascun provider |
| [Runbook di release](docs/releases/v0.1.0-rc.1.md) | Il release gate con store reali per i tre provider |

<a id="development"></a>

## Sviluppo

Usa la toolchain Go dichiarata in `go.mod`. Avvia PostgreSQL, applica le migrazioni ed
esegui API e worker in un unico processo:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

Il server richiede anche le chiavi di protezione dell'[avvio rapido](#quick-start); ogni
impostazione è elencata nel [riferimento di configurazione](docs/operations.md#configuration-reference).
L'API è in ascolto su `:8080`, espone `/healthz` e `/readyz` e integra la dashboard
in `/dashboard/`.

Esegui i controlli del server e il release gate end-to-end:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

La [guida per contribuire](CONTRIBUTING.md) contiene i controlli degli SDK, la
configurazione dei test di integrazione, una panoramica dell'architettura e le
convenzioni del codice.

<a id="repository-layout"></a>

### Struttura del repository

```text
.
├── cmd/iapstack/       # Entrypoint di API, worker e migrazioni
├── internal/
│   ├── core/           # Clienti, transazioni, prodotti ed entitlement
│   ├── verification/   # Verifica degli acquisti indipendente dal provider
│   ├── stores/         # Adapter per Apple, Google Play e Huawei
│   ├── credentials/    # Credenziali protette delle applicazioni
│   ├── protection/     # Port per la protezione dei dati sensibili
│   ├── persistence/    # Repository PostgreSQL
│   └── platform/       # Implementazioni di configurazione, logging e keyring
├── dashboard/          # Dashboard operativa integrata
├── contracts/          # Contratti OpenAPI e dei webhook
├── sdk/                # SDK Flutter, iOS, Android, React Native, Go e TypeScript
├── deploy/             # Definizioni per Compose, cloud, VM e Kubernetes
└── docs/               # Guide, ADR e runbook di release
```

<a id="roadmap"></a>

## Roadmap

- [x] Dominio indipendente dagli store, persistenza PostgreSQL e contratto OpenAPI versionato
- [x] Adapter per Apple App Store, Google Play e Huawei AppGallery
- [x] Riconciliazione del ciclo di vita e webhook in uscita firmati
- [x] Dashboard operativa e definizioni di deployment per cloud, VM e Kubernetes
- [x] SDK Flutter, Swift, Kotlin, React Native, Go e TypeScript
- [x] Migrazione dei clienti, importazioni da RevenueCat, offering e report finanziari Apple (`v0.2.0-alpha.1`)
- [ ] Superare i gate con store reali per i tre provider e pubblicare `v0.1.0-rc.1`
- [ ] Pubblicare la versione stabile `v0.1.0`
- [ ] Pubblicare pacchetti SDK versionati ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Riconoscimento dei report finanziari di Google Play e Huawei
- [ ] Amazon Appstore e altri adapter per store

Gli elementi spuntati sono implementati nel repository; non implicano che siano pronti
per la produzione.

<a id="contributing"></a>

## Contribuire

Segnalazioni di bug, correzioni ed esperienza con gli store sono benvenute. Leggi la
[guida per contribuire](CONTRIBUTING.md) prima di aprire una pull request, la
[guida al supporto](SUPPORT.md) per le domande e la [policy di sicurezza](SECURITY.md)
per segnalare una vulnerabilità. La partecipazione è regolata dal
[Codice di condotta](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Licenza

[Apache License 2.0](LICENSE)
