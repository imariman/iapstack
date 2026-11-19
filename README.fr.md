<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascotte Gopher d’IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Vérification des achats intégrés et droits d’accès, open source et auto-hébergés.</strong>
  <br>
  Un modèle unique, porté par votre backend, pour Apple App Store, Google Play et Huawei AppGallery.
  <br>
  Écrit en Go. Indépendant des stores. Déployable partout.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/fr/"><strong>Site web</strong></a>
  ·
  <a href="#quick-start"><strong>Démarrage rapide</strong></a>
  ·
  <a href="#deploy"><strong>Déploiement</strong></a>
  ·
  <a href="#documentation"><strong>Documentation</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Contribuer</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="Tableau de bord d’exploitation IAPStack : état du parcours d’achat, activité de vérification avec un jour survolé, revenus des stores comptabilisés et valeur de test sandbox pour un projet de démonstration" width="100%">
  </picture>
</p>

<p align="center">
  <sub>Le tableau de bord d’exploitation, qui suit le thème de votre système (Système / Clair / Sombre dans le produit). Données de démonstration synthétiques.</sub>
</p>

> [!NOTE]
> Ceci est une traduction du [README en anglais](README.md). En cas de divergence, la version anglaise fait foi. Le développement, l’organisation du dépôt et les instructions de déploiement pas à pas ne sont maintenus qu’en anglais.

> [!IMPORTANT]
> IAPStack est un logiciel en préversion. Les versions existantes `v0.1.0-rc1` et
> `v0.1.0-rc2` sont d’anciennes préversions antérieures au contrôle de publication
> actuel et aux correctifs de sécurité ; elles ne sont pas prises en charge en
> production. La branche par défaut reste en développement, et son contrôle du cycle
> de vie sur les vrais stores des trois fournisseurs n’est pas encore terminé.

IAPStack est un backend auto-hébergé doté d’un panneau de contrôle, qui valide les achats
intégrés et transforme les transactions des stores en droits d’accès durables pour votre
application. Il offre aux équipes un modèle unique, porté par leur backend, pour tous les
stores mobiles, tout en conservant identifiants, données d’achat et décisions d’accès sur
une infrastructure qu’elles contrôlent.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Vérifier une fois, partout | Des droits d’accès, pas des reçus | À vous de l’exploiter |
| --- | --- | --- |
| Vérification côté serveur pour Apple App Store, Google Play et Huawei AppGallery derrière un modèle unique indépendant des stores. | Les apps demandent à quoi un client a accès au lieu d’interpréter des reçus bruts et des états propres aux stores. | Auto-hébergé par défaut : l’infrastructure, les identifiants, les données d’achat et les décisions d’accès vous appartiennent. |

<a id="current-scope"></a>

## Périmètre actuel

- Vérification côté serveur des abonnements et des achats à vie non consommables
  depuis Apple App Store, Google Play et Huawei AppGallery
- Un modèle de transactions et de droits d’accès indépendant des stores, reposant sur PostgreSQL
- Réception durable des notifications, réconciliation du cycle de vie et webhooks sortants signés
- Un tableau de bord d’exploitation authentifié (thèmes Système / Clair / Sombre, barre de
  projets repliable, graphiques d’activité et d’abonnements consultables en détail) avec des
  contrats HTTP/OpenAPI versionnés
- Des packages Flutter neutres et spécifiques à chaque store pour les trois fournisseurs actuels
- Déploiement compact ou séparé API/worker avec Docker et des cibles cloud documentées

La livraison des achats consommables, la migration de clients et la consolidation d’alias,
l’exploitation hébergée et les stores au-delà des trois ci-dessus sont hors du périmètre de
la v0.1. Consultez le [périmètre de la v0.1](docs/v0.1-scope.md) pour la délimitation complète.

<a id="sdk-roadmap"></a>

## Feuille de route des SDK

Flutter reste le seul client mobile dans le périmètre de publication de la v0.1 : un client
HTTP neutre vis-à-vis des fournisseurs, accompagné de compagnons Apple, Google Play et Huawei.
Du code SDK supplémentaire se trouve déjà dans le dépôt, conformément au plan d’extension de
[#85](https://github.com/imariman/iapstack/issues/85). Sa présence n’élargit pas le contrôle
de publication de la v0.1 et ne fait pas de ces packages des versions de production.

| SDK | Implémenté dans le dépôt | Travail restant |
| --- | --- | --- |
| [Hôte de confiance Go](sdk/go/README.md) | Sessions client, consultation des droits d’accès côté serveur et traitement des webhooks signés | Première publication d’un module versionné ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Hôte de confiance TypeScript](sdk/typescript/README.md) | Sessions client, consultation des droits d’accès, webhooks signés et archives ESM/types installables | Première publication sur npm ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | Client HTTP, compagnon StoreKit 2, exemple exécutable avec configuration StoreKit, manifeste SwiftPM à la racine | Premier tag de version du SDK ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | Client HTTP, adaptateurs Play Billing/HMS, exemple d’hôte de confiance et binaires Maven/JAR de sources préparés | Première publication sur GitHub Maven ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | Client HTTP, pont natif StoreKit/Play/Huawei, app d’exemple native et sources compagnons empaquetées | Première publication sur npm ([#141](https://github.com/imariman/iapstack/issues/141)) |

Le [guide de publication des SDK](docs/sdk-releases.md) documente l’installation, les
préversions indépendantes, les vérifications de packages et la configuration des registres.
La présence de code dans ce dépôt n’implique pas qu’une version existe déjà dans un registre.

Le jeton bearer durable de l’application reste sur un backend hôte de confiance et ne doit
jamais être livré dans un binaire mobile ni dans un bundle navigateur. Les clients mobiles
utilisent des sessions client de courte durée.

<a id="quick-start"></a>

## Démarrage rapide

Il vous faut Docker avec Compose et OpenSSL. Depuis un clone de ce dépôt, générez des
secrets purement locaux et lancez la stack complète :

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

Conservez les valeurs générées en lieu sûr et réutilisez-les avec ce volume Compose.
Les régénérer n’effectue pas de rotation du mot de passe de base de données ni des clés de
protection existants.

Ouvrez ensuite `http://127.0.0.1:8080/dashboard/` et connectez-vous avec la clé
d’administration d’amorçage. Créez et conservez une clé d’administration durable, retirez
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` de l’environnement et recréez le conteneur de l’API.
Le [guide d’exploitation](docs/operations.md) explique l’amorçage, la rotation des clés,
l’arrêt, les sauvegardes, les mises à niveau et le socle de sécurité pour la production.

<a id="deploy"></a>

## Déploiement

Comparez les exigences de compte, les limites des offres gratuites et le coût de la stack
complète dans la [matrice des cibles de déploiement](deploy/README.md) avant de créer des
ressources chez un fournisseur.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Cible | Architecture | Guide |
| --- | --- | --- |
| Render | Un service web gratuit exécutant l’API et le worker, une base PostgreSQL gratuite pour les tests, et des Blueprints payants compacts ou séparés | [Guide Render](deploy/render/README.md) |
| Heroku | API, worker et PostgreSQL géré sur deux dynos Basic toujours actifs ; pas d’exécution gratuite | [Guide Heroku](deploy/heroku/README.md) |
| DigitalOcean | App Platform avec un worker et une tâche de migration, ou un Droplet moins coûteux | [Guide App Platform](deploy/digitalocean/README.md) · [Guide Droplet](deploy/digitalocean/droplet.md) |
| Fly.io | Groupes de processus avec Managed Postgres privé ; courte période d’essai gratuite uniquement | [Guide Fly.io](deploy/fly/README.md) |
| Koyeb | Base de données gérée, API HTTPS et worker privé via la CLI officielle | [Guide Koyeb](deploy/koyeb/README.md) |
| Railway | PostgreSQL et un service exécutant l’API et le worker ; environ 5 USD par mois avec l’offre Hobby | [Guide Railway](deploy/railway/README.md) |
| Coolify | La stack complète à partir d’une définition Docker Compose fournie par le dépôt, sur votre propre serveur | [Guide Coolify](deploy/coolify/README.md) |
| VM unique | L’option continue la moins chère : PostgreSQL, migrations, API et worker sur une seule machine Linux derrière un Cloudflare Tunnel | [Guide VM unique](deploy/vps/README.md) |

### Option la moins coûteuse : une VM unique

Une seule machine Linux exécute tout à partir de
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml) : PostgreSQL sur un réseau Docker
privé, un conteneur de migration à exécution unique, un processus compact contenant l’API et
le worker durable, et un connecteur Cloudflare Tunnel, si bien que la VM n’a besoin d’aucun
port entrant ouvert. Le worker nécessite un processus qui reste en cours d’exécution ; c’est
pourquoi une petite VM toujours active convient mieux que les plateformes qui réduisent à zéro.

| Hôte | Coût typique | Adéquation |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | 0 USD | Jusqu’à 4 OCPU Arm et 24 Go de RAM ; stack Resource Manager en un clic. |
| Google Cloud e2-micro (Free Tier, régions US) | 0–4 USD/mois | 1 Go de RAM pour un faible trafic ; procédure guidée dans Cloud Shell. |
| Hetzner Cloud CAX11 / CX22 | Environ 4–5 EUR/mois | Facturation prévisible ; installation à partir d’un seul fichier cloud-init. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | 6 / 12 USD par mois | IPv4 publique incluse ; même fichier cloud-init que pour Hetzner. |
| Un serveur que vous exploitez déjà | 0 USD | Fonctionne derrière un NAT, mais les notifications des stores exigent une disponibilité continue. |

Il vous faut aussi un domaine dont le DNS est hébergé sur l’offre gratuite de Cloudflare.
Vérifiez les conditions en vigueur de chaque fournisseur avant de créer des ressources. Les
étapes d’installation, les sauvegardes et les mises à niveau sont décrites dans la
[section Deploy du README en anglais](README.md#deploy) et dans le
[guide VM unique](deploy/vps/README.md).

<a id="design-principles"></a>

## Principes de conception

**Auto-hébergé par défaut.** L’infrastructure, les identifiants, les données d’achat et les décisions d’accès vous appartiennent.

**Les stores sont des adaptateurs.** Les API propres à chaque store restent isolées derrière des contrats communs ; le domaine central ne dépend d’aucune place de marché en particulier.

**Les droits d’accès sont la frontière du produit.** Les applications demandent à quoi un client a accès au lieu d’interpréter des reçus bruts ou des états propres aux stores.

**Déployable partout.** L’objectif est un déploiement simple en conteneurs plutôt qu’une dépendance à une plateforme cloud particulière.

**Implémentation indépendante.** La documentation officielle des stores définit le comportement et les exigences de sécurité. D’autres projets open source peuvent être étudiés pour l’interopérabilité et les cas limites, mais le code d’IAPStack est implémenté de manière indépendante.

<a id="documentation"></a>

## Documentation

La documentation est rédigée en anglais.

- [Périmètre et architecture de la v0.1](docs/v0.1-scope.md)
- [Architecture Decision Records](docs/adr/README.md)
- [API HTTP v1 et contrats de notification des fournisseurs](docs/api-v1.md)
- [Contrat OpenAPI v1 lisible par machine](contracts/openapi/v1.yaml)
- [Guide d’exploitation et du tableau de bord](docs/operations.md)
- [Modèle de menaces et liste de contrôle de sécurité de la v0.1](docs/security.md)
- [Politique de sécurité et signalement des vulnérabilités](SECURITY.md)
- [Guide de contribution](CONTRIBUTING.md)
- [Guide d’assistance](SUPPORT.md)
- [SDK Flutter neutre vis-à-vis des fournisseurs](sdk/flutter/iapstack/README.md)
- [SDK Swift natif pour iOS et compagnon StoreKit](sdk/ios/README.md)
- [SDK Go pour hôte de confiance](sdk/go/README.md)
- [SDK TypeScript pour hôte de confiance](sdk/typescript/README.md)

<a id="development"></a>

## Développement

La compilation depuis les sources, la configuration, les tests et l’organisation du dépôt
sont décrits dans la [section Development du README en anglais](README.md#development).
Lisez le [guide de contribution](CONTRIBUTING.md) avant d’ouvrir une pull request.

<a id="roadmap"></a>

## Feuille de route

- [x] Définir le domaine central, les contrats d’API publics et le modèle de persistance
- [x] Implémenter les premiers adaptateurs Apple App Store, Google Play et Huawei AppGallery
- [x] Ajouter les migrations PostgreSQL et une configuration Docker proche de la production
- [x] Construire le SDK Flutter neutre et les trois SDK Flutter spécifiques aux fournisseurs, ainsi que le tableau de bord
- [x] Prendre en charge la réconciliation du cycle de vie et les webhooks sortants signés
- [x] Publier un contrat d’API v1 lisible par machine avec des vérifications de compatibilité des clients
- [x] Implémenter les SDK Go, TypeScript, Swift, Kotlin et React Native
- [x] Prendre en charge la migration de clients, la consolidation d’alias, les imports RevenueCat et les imports de rapports financiers
- [x] Ajouter des manifestes de déploiement Kubernetes
- [ ] Terminer les contrôles de publication du cycle de vie Apple App Store, Google Play et Huawei, puis publier `v0.1.0-rc.1`
- [ ] Traiter les retours sur la version candidate et publier la version stable `v0.1.0`
- [ ] Publier des packages versionnés pour l’hôte, Android, iOS et React Native ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Ajouter Amazon Appstore et d’autres adaptateurs de store

Les éléments cochés décrivent des implémentations présentes dans le dépôt, et non une disponibilité pour une publication en production.

<a id="license"></a>

## Licence

Distribué sous la [licence Apache 2.0](LICENSE).

En participant à ce projet, vous acceptez de respecter le
[code de conduite](CODE_OF_CONDUCT.md). Pour contribuer ou obtenir de l’aide, consultez
[CONTRIBUTING.md](CONTRIBUTING.md) et [SUPPORT.md](SUPPORT.md).
