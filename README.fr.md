<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascotte Gopher d’IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Vérification des achats intégrés et droits d’accès, auto-hébergés.</strong>
  <br>
  Un modèle unique, porté par votre backend, pour Apple App Store, Google Play et Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/fr/">Site web</a>
  ·
  <a href="#quick-start">Démarrage rapide</a>
  ·
  <a href="#deploy">Déploiement</a>
  ·
  <a href="#documentation">Documentation</a>
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
    · <strong>Français</strong>
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
    <img src="assets/dashboard-light.png" alt="Tableau de bord d’exploitation IAPStack : état du parcours d’achat, activité de vérification avec un jour survolé, revenus des stores comptabilisés et valeur de test sandbox pour un projet de démonstration" width="100%">
  </picture>
</p>

> [!NOTE]
> Ceci est une traduction du [README en anglais](README.md). En cas de divergence, la version anglaise fait foi.

IAPStack est un backend doté d’un panneau de contrôle, qui vérifie les achats intégrés
côté serveur et transforme les transactions des stores en droits d’accès durables. Vos
apps posent une seule question, *à quoi ce client a-t-il accès ?*, et la réponse est la
même quel que soit le store d’où provient l’achat. Identifiants, données d’achat et
décisions d’accès restent sur une infrastructure que vous exploitez.

| Vérifier une fois, partout | Des droits d’accès, pas des reçus | À vous de l’exploiter |
| --- | --- | --- |
| Vérification côté serveur pour trois stores derrière un modèle unique indépendant des stores. Les API des stores restent isolées dans des adaptateurs. | Les apps lisent l’état des accès au lieu d’interpréter des reçus bruts et des statuts propres à chaque store. | Un seul binaire Go et PostgreSQL. Fonctionne sous Docker chez n’importe quel fournisseur cloud ou sur une VM dont vous disposez déjà. |

> [!IMPORTANT]
> IAPStack est un logiciel en préversion, pas encore pris en charge en production.
> Les versions `v0.1.0-rc1` et `v0.1.0-rc2` sont d’anciennes préversions ; la première
> version candidate prise en charge sera publiée une fois les contrôles du cycle de vie
> sur les vrais stores validés pour les trois fournisseurs.

<a id="features"></a>

## Fonctionnalités

- **Vérification** des abonnements et des achats à vie non consommables depuis Apple
  App Store, Google Play et Huawei AppGallery
- **Suivi du cycle de vie** grâce à la réception durable des notifications des stores et
  à une réconciliation planifiée
- **Webhooks sortants signés**, pour que votre backend soit informé de chaque changement
  de droit d’accès
- **Tableau de bord d’exploitation** pour les applications, les clients, les files
  d’attente et les livraisons de webhooks, en thème clair et sombre
- **API HTTP versionnée** avec un [contrat OpenAPI](contracts/openapi/v1.yaml) lisible par machine
- **SDK clients** pour Flutter, iOS, Android et React Native, ainsi que des SDK Go et
  TypeScript pour votre propre backend
- **Topologie flexible** : un seul processus compact, ou des services API et worker séparés

La livraison des achats consommables, l’exploitation hébergée et les stores au-delà de
ces trois-là sont hors du périmètre de la v0.1. La migration de clients, les imports
RevenueCat, les offerings et les rapports financiers Apple existent déjà sur `main` et
sont proposés en avant-première dans
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
mais ils ne font pas partie du contrôle de publication de la v0.1. Consultez le
[périmètre de la v0.1](docs/v0.1-scope.md) pour la délimitation complète.

<a id="quick-start"></a>

## Démarrage rapide

Il vous faut Docker avec Compose et OpenSSL. Depuis un clone de ce dépôt, générez des
secrets locaux et lancez la stack :

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

Conservez ces valeurs : le volume Compose en dépend, et les régénérer n’effectue pas de
rotation du mot de passe de base de données ni des clés de protection existants.

Ouvrez `http://127.0.0.1:8080/dashboard/` et connectez-vous avec la clé d’administration
d’amorçage. Créez une clé d’administration durable, puis retirez
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` et recréez le conteneur de l’API. Le
[guide d’exploitation](docs/operations.md) couvre le reste : rotation des clés,
sauvegardes, mises à niveau et socle de sécurité pour la production.

<a id="deploy"></a>

## Déploiement

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Chaque cible exécute la même image de conteneur avec PostgreSQL, une étape de migration,
l’API et un worker qui doit rester actif en permanence. Comparez les exigences de compte,
les limites des offres gratuites et le coût de la stack complète dans la
[matrice des cibles de déploiement](deploy/README.md) avant de créer des ressources.

| Cible | Architecture | Guide |
| --- | --- | --- |
| **VM unique** (Oracle, Google Cloud, Hetzner, DigitalOcean, votre propre serveur) | L’option continue la moins chère : la stack complète sur une seule machine Linux derrière un Cloudflare Tunnel, sans aucun port entrant ouvert. À partir de 0 USD par mois avec Oracle Cloud Always Free. | [Guide VM unique](deploy/vps/README.md) |
| Render | Service compact gratuit et base PostgreSQL éphémère pour l’évaluation ; Blueprints payants compacts ou séparés | [Render](deploy/render/README.md) |
| Railway | PostgreSQL et un service exécutant l’API et le worker ; tient généralement dans l’offre Hobby à 5 USD | [Railway](deploy/railway/README.md) |
| Heroku | API, worker et PostgreSQL géré sur deux dynos Basic ; pas d’offre gratuite | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, worker et tâche de migration à partir d’une App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Groupes de processus avec Managed Postgres privé | [Fly.io](deploy/fly/README.md) |
| Koyeb | Base de données gérée, API HTTPS et worker privé via la CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | La stack complète à partir de la définition Compose du dépôt, sur votre serveur | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Manifestes simples pour l’API, le worker et la tâche de migration ; PostgreSQL à fournir vous-même | [Kubernetes](deploy/k8s/README.md) |

Les plateformes serverless ou qui réduisent à zéro conviennent mal : le worker reçoit les
notifications des stores, livre les webhooks et exécute la réconciliation chaque minute.

<a id="sdks"></a>

## SDK

Les apps mobiles communiquent avec IAPStack au moyen de sessions client de courte durée.
La clé d’application durable reste sur votre backend et ne doit jamais être livrée dans un
binaire mobile ni dans un bundle navigateur.

| Plateforme | Package | Statut |
| --- | --- | --- |
| Flutter | [Client neutre vis-à-vis des fournisseurs](sdk/flutter/iapstack/README.md) avec des compagnons [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) et [Huawei](sdk/flutter/iapstack_huawei/README.md) | Périmètre de publication de la v0.1 |
| iOS (Swift) | [Client HTTP et compagnon StoreKit 2](sdk/ios/README.md) | Implémenté, pas encore publié |
| Android (Kotlin) | [Client HTTP avec adaptateurs Play Billing et HMS](sdk/android/README.md) | Implémenté, pas encore publié |
| React Native | [Client HTTP et pont natif StoreKit/Play/Huawei](sdk/react-native/README.md) | Implémenté, pas encore publié |
| Go (backend) | [Sessions client, consultation des droits d’accès, vérification des webhooks](sdk/go/README.md) | Implémenté, pas encore publié |
| TypeScript (backend) | [Sessions client, consultation des droits d’accès, vérification des webhooks](sdk/typescript/README.md) | Implémenté, pas encore publié |

La publication dans les registres est suivie dans [#141](https://github.com/imariman/iapstack/issues/141).
Le [guide de publication des SDK](docs/sdk-releases.md) couvre l’installation depuis les
sources, la gestion des versions et l’automatisation des publications.

<a id="documentation"></a>

## Documentation

La documentation est rédigée en anglais.

| Guide | Contenu |
| --- | --- |
| [Exploitation](docs/operations.md) | Secrets, configuration, tableau de bord, configuration des notifications des stores, sauvegardes, mises à niveau, publications |
| [API HTTP v1](docs/api-v1.md) | Points de terminaison, identifiants des fournisseurs, notifications, vérification des webhooks |
| [Périmètre et architecture de la v0.1](docs/v0.1-scope.md) | Contexte du système, invariants fondamentaux, tranches par fournisseur |
| [Sécurité](docs/security.md) | Modèle de menaces et liste de contrôle de sécurité pour les publications |
| [Décisions d’architecture](docs/adr/README.md) | Pourquoi les principales frontières existent |
| [Cibles de déploiement](deploy/README.md) | Comparatif des fournisseurs et guides par fournisseur |
| [Runbook de publication](docs/releases/v0.1.0-rc.1.md) | Le contrôle de publication sur les vrais stores des trois fournisseurs |

<a id="development"></a>

## Développement

Utilisez la toolchain Go déclarée dans `go.mod`. Démarrez PostgreSQL, appliquez les
migrations et exécutez l’API et le worker dans un seul processus :

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

Le serveur a aussi besoin des clés de protection du [démarrage rapide](#quick-start) ;
tous les paramètres figurent dans la [référence de configuration](docs/operations.md#configuration-reference).
L’API écoute sur `:8080`, sert `/healthz` et `/readyz`, et intègre le tableau de bord
sous `/dashboard/`.

Lancez les vérifications du serveur et le contrôle de publication de bout en bout :

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

Le [guide de contribution](CONTRIBUTING.md) présente les vérifications des SDK, la mise en
place des tests d’intégration, une vue d’ensemble de l’architecture et les conventions de
code.

<a id="repository-layout"></a>

### Organisation du dépôt

```text
.
├── cmd/iapstack/       # Points d’entrée de l’API, du worker et des migrations
├── internal/
│   ├── core/           # Clients, transactions, produits et droits d’accès
│   ├── verification/   # Vérification des achats neutre vis-à-vis des fournisseurs
│   ├── stores/         # Adaptateurs Apple, Google Play et Huawei
│   ├── credentials/    # Identifiants d’application protégés
│   ├── protection/     # Port de protection des données sensibles
│   ├── persistence/    # Dépôts PostgreSQL
│   └── platform/       # Implémentations de configuration, de journalisation et de trousseau
├── dashboard/          # Tableau de bord d’exploitation intégré
├── contracts/          # Contrats OpenAPI et webhooks
├── sdk/                # SDK Flutter, iOS, Android, React Native, Go et TypeScript
├── deploy/             # Définitions Compose, cloud, VM et Kubernetes
└── docs/               # Guides, ADR et runbooks de publication
```

<a id="roadmap"></a>

## Feuille de route

- [x] Domaine indépendant des stores, persistance PostgreSQL et contrat OpenAPI versionné
- [x] Adaptateurs Apple App Store, Google Play et Huawei AppGallery
- [x] Réconciliation du cycle de vie et webhooks sortants signés
- [x] Tableau de bord d’exploitation et définitions de déploiement pour le cloud, les VM et Kubernetes
- [x] SDK Flutter, Swift, Kotlin, React Native, Go et TypeScript
- [x] Migration de clients, imports RevenueCat, offerings et rapports financiers Apple (`v0.2.0-alpha.1`)
- [x] Métriques d'abonnement, outils de support client et réponses de consommation pour les remboursements Apple (sur `main`, pas encore publiés)
- [ ] Valider les contrôles sur les vrais stores des trois fournisseurs et publier `v0.1.0-rc.1`
- [ ] Publier la version stable `v0.1.0`
- [ ] Publier des packages SDK versionnés ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Endpoint MCP permettant aux agents IA d'interroger et de piloter IAPStack avec des clés à privilèges minimaux ([#285](https://github.com/imariman/iapstack/issues/285))
- [ ] Prise en compte des rapports financiers Google Play et Huawei
- [ ] Amazon Appstore et d’autres adaptateurs de store

Les éléments cochés sont implémentés dans le dépôt ; ils n’impliquent pas une disponibilité
pour la production.

<a id="contributing"></a>

## Contribuer

Les signalements de bugs, les correctifs et l’expertise des stores sont les bienvenus.
Lisez le [guide de contribution](CONTRIBUTING.md) avant d’ouvrir une pull request, le
[guide d’assistance](SUPPORT.md) pour vos questions et la [politique de sécurité](SECURITY.md)
pour signaler une vulnérabilité. La participation est régie par le
[code de conduite](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Licence

[Licence Apache 2.0](LICENSE)
