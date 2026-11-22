<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascota Gopher de IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verificación de compras dentro de la app y derechos de acceso, autoalojada.</strong>
  <br>
  Un único modelo gestionado por el backend para Apple App Store, Google Play y Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/es/">Sitio web</a>
  ·
  <a href="#quick-start">Inicio rápido</a>
  ·
  <a href="#deploy">Despliegue</a>
  ·
  <a href="#documentation">Documentación</a>
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
    · <strong>Español</strong>
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
    <img src="assets/dashboard-light.png" alt="Panel de operaciones de IAPStack: estado de la ruta de compra, actividad de verificación con un día resaltado, ingresos reconocidos de las tiendas y valor de pruebas en sandbox de un proyecto de demostración" width="100%">
  </picture>
</p>

> [!NOTE]
> Este documento es una traducción del [README en inglés](README.md). Si ambos difieren, prevalece la versión en inglés.

IAPStack es un backend con panel de control que verifica las compras dentro de la app en
el servidor y convierte las transacciones de las tiendas en derechos de acceso duraderos.
Tus apps hacen una sola pregunta, *¿a qué puede acceder este cliente?*, y la respuesta es
la misma sea cual sea la tienda en la que se hizo la compra. Las credenciales, los datos de
compra y las decisiones de acceso permanecen en infraestructura que tú gestionas.

| Verifica una vez, en todas partes | Derechos de acceso, no recibos | Tú lo ejecutas |
| --- | --- | --- |
| Verificación en el servidor para tres tiendas detrás de un único modelo neutral respecto a la tienda. Las API de las tiendas permanecen aisladas en adaptadores. | Las apps leen el estado de acceso en lugar de interpretar recibos sin procesar y estados específicos de cada tienda. | Un único binario de Go y PostgreSQL. Se ejecuta en Docker en cualquier nube o en una VM que ya tengas. |

> [!IMPORTANT]
> IAPStack es software en fase de prelanzamiento y todavía no tiene soporte para producción.
> Las versiones `v0.1.0-rc1` y `v0.1.0-rc2` son versiones preliminares antiguas; la primera
> versión candidata con soporte se publicará cuando se superen los controles del ciclo de
> vida con tiendas reales para los tres proveedores.

<a id="features"></a>

## Funciones

- **Verificación** de suscripciones y compras vitalicias no consumibles de Apple
  App Store, Google Play y Huawei AppGallery
- **Seguimiento del ciclo de vida** mediante la ingesta duradera de notificaciones de las
  tiendas y la conciliación programada
- **Webhooks salientes firmados** para que tu backend se entere de cada cambio en los
  derechos de acceso
- **Panel de operaciones** para aplicaciones, clientes, colas y entregas de webhooks,
  con temas claro y oscuro
- **API HTTP versionada** con un [contrato OpenAPI](contracts/openapi/v1.yaml) legible por máquinas
- **SDK de cliente** para Flutter, iOS, Android y React Native, además de SDK de Go y
  TypeScript para tu propio backend
- **Topología flexible**: un único proceso compacto, o servicios de API y worker separados

El suministro de consumibles, la operación alojada y las tiendas distintas de estas tres
quedan fuera del alcance de la v0.1. La migración de clientes, las importaciones desde
RevenueCat, las ofertas (offerings) y los informes financieros de Apple ya existen en `main`
y se presentan de forma preliminar en
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
pero no forman parte del control de publicación de la v0.1. Consulta el
[alcance de la v0.1](docs/v0.1-scope.md) para ver el límite completo.

<a id="quick-start"></a>

## Inicio rápido

Necesitas Docker con Compose y OpenSSL. Desde un clon de este repositorio, genera secretos
locales e inicia el stack:

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

Guarda estos valores: el volumen de Compose depende de ellos, y volver a generarlos no rota
la contraseña de la base de datos ni las claves de protección existentes.

Abre `http://127.0.0.1:8080/dashboard/`, elige «Use an admin or bootstrap key instead»
y conéctate con la clave de administrador de arranque. Crea tu cuenta de administrador
en el aviso de configuración; a partir de entonces inicias sesión con ese nombre de
usuario y esa contraseña. Luego elimina `IAPSTACK_BOOTSTRAP_ADMIN_KEY` y vuelve a crear
el contenedor de la API. La
[guía de operaciones](docs/operations.md) cubre el resto: la rotación de claves, las copias
de seguridad, las actualizaciones y la base de seguridad para producción.

<a id="deploy"></a>

## Despliegue

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Todos los destinos ejecutan la misma imagen de contenedor con PostgreSQL, un paso de
migración, la API y un worker que debe mantenerse en ejecución. Compara los requisitos de
cuenta, los límites de los planes gratuitos y el coste del stack completo en la
[matriz de destinos de despliegue](deploy/README.md) antes de crear recursos.

| Destino | Configuración | Guía |
| --- | --- | --- |
| **VM única** (Oracle, Google Cloud, Hetzner, DigitalOcean, tu propio servidor) | La opción continua más barata: el stack completo en una sola máquina Linux detrás de un Cloudflare Tunnel, sin puertos de entrada abiertos. Desde USD 0 al mes en Oracle Cloud Always Free. | [Guía de VM única](deploy/vps/README.md) |
| Render | Servicio compacto gratuito y PostgreSQL desechable para evaluación; Blueprints de pago compactos o separados | [Render](deploy/render/README.md) |
| Railway | PostgreSQL y un único servicio de API/worker; normalmente dentro del plan Hobby de USD 5 | [Railway](deploy/railway/README.md) |
| Heroku | API, worker y PostgreSQL gestionado en dos dynos Basic; sin plan gratuito | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, worker y job de migración a partir de una App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Grupos de procesos con Managed Postgres privado | [Fly.io](deploy/fly/README.md) |
| Koyeb | Base de datos gestionada, API HTTPS y worker privado mediante la CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | El stack completo a partir de la definición de Compose del repositorio, en tu servidor | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Manifiestos simples para la API, el worker y el job de migración; aporta tu propio PostgreSQL | [Kubernetes](deploy/k8s/README.md) |

Las plataformas serverless o que escalan a cero no encajan bien: el worker ingiere las
notificaciones de las tiendas, entrega los webhooks y ejecuta la conciliación cada minuto.

<a id="sdks"></a>

## SDK

Las apps móviles se comunican con IAPStack mediante sesiones de cliente de corta duración.
La clave duradera de la aplicación permanece en tu backend y nunca debe incluirse en un
binario móvil ni en un bundle de navegador.

| Plataforma | Paquete | Estado |
| --- | --- | --- |
| Flutter | [Cliente neutral respecto al proveedor](sdk/flutter/iapstack/README.md) con complementos para [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) y [Huawei](sdk/flutter/iapstack_huawei/README.md) | Alcance de la versión v0.1 |
| iOS (Swift) | [Cliente HTTP y complemento para StoreKit 2](sdk/ios/README.md) | Implementado, aún sin publicar |
| Android (Kotlin) | [Cliente HTTP con adaptadores para Play Billing y HMS](sdk/android/README.md) | Implementado, aún sin publicar |
| React Native | [Cliente HTTP y puente nativo para StoreKit/Play/Huawei](sdk/react-native/README.md) | Implementado, aún sin publicar |
| Go (backend) | [Sesiones de cliente, consulta de derechos de acceso, verificación de webhooks](sdk/go/README.md) | Implementado, aún sin publicar |
| TypeScript (backend) | [Sesiones de cliente, consulta de derechos de acceso, verificación de webhooks](sdk/typescript/README.md) | Implementado, aún sin publicar |

La publicación en los registros se sigue en [#141](https://github.com/imariman/iapstack/issues/141).
La [guía de publicación de los SDK](docs/sdk-releases.md) cubre la instalación desde el
código fuente, el versionado y la automatización de las publicaciones.

<a id="documentation"></a>

## Documentación

La documentación está escrita en inglés.

| Guía | Qué cubre |
| --- | --- |
| [Operaciones](docs/operations.md) | Secretos, configuración, panel, configuración de notificaciones de las tiendas, copias de seguridad, actualizaciones, publicaciones |
| [API HTTP v1](docs/api-v1.md) | Endpoints, credenciales de proveedores, notificaciones, verificación de webhooks |
| [Alcance y arquitectura de la v0.1](docs/v0.1-scope.md) | Contexto del sistema, invariantes centrales, segmentos por proveedor |
| [Seguridad](docs/security.md) | Modelo de amenazas y lista de comprobación de seguridad para la publicación |
| [Decisiones de arquitectura](docs/adr/README.md) | Por qué existen los límites principales |
| [Destinos de despliegue](deploy/README.md) | Comparación de proveedores y guías por proveedor |
| [Manual de publicación](docs/releases/v0.1.0-rc.1.md) | El control de publicación con tiendas reales para los tres proveedores |

<a id="development"></a>

## Desarrollo

Usa la toolchain de Go declarada en `go.mod`. Inicia PostgreSQL, aplica las migraciones y
ejecuta la API y el worker en un solo proceso:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

El servidor también necesita las claves de protección del [inicio rápido](#quick-start);
todos los ajustes se enumeran en la [referencia de configuración](docs/operations.md#configuration-reference).
La API escucha en `:8080`, sirve `/healthz` y `/readyz`, e integra el panel en
`/dashboard/`.

Ejecuta las comprobaciones del servidor y el control de publicación de extremo a extremo:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

La [guía de contribución](CONTRIBUTING.md) incluye las comprobaciones de los SDK, la
configuración de las pruebas de integración, una visión general de la arquitectura y las
convenciones de código.

<a id="repository-layout"></a>

### Estructura del repositorio

```text
.
├── cmd/iapstack/       # Puntos de entrada de la API, el worker y las migraciones
├── internal/
│   ├── core/           # Clientes, transacciones, productos y derechos de acceso
│   ├── verification/   # Verificación de compras neutral respecto al proveedor
│   ├── stores/         # Adaptadores de Apple, Google Play y Huawei
│   ├── credentials/    # Credenciales protegidas de la aplicación
│   ├── protection/     # Puerto de protección de datos sensibles
│   ├── persistence/    # Repositorios de PostgreSQL
│   └── platform/       # Implementaciones de configuración, registro y llavero
├── dashboard/          # Panel de operaciones integrado
├── contracts/          # Contratos de OpenAPI y webhooks
├── sdk/                # SDK de Flutter, iOS, Android, React Native, Go y TypeScript
├── deploy/             # Definiciones para Compose, la nube, VM y Kubernetes
└── docs/               # Guías, ADR y manuales de publicación
```

<a id="roadmap"></a>

## Hoja de ruta

- [x] Dominio neutral respecto a la tienda, persistencia en PostgreSQL y contrato OpenAPI versionado
- [x] Adaptadores de Apple App Store, Google Play y Huawei AppGallery
- [x] Conciliación del ciclo de vida y webhooks salientes firmados
- [x] Panel de operaciones y definiciones de despliegue para la nube, VM y Kubernetes
- [x] SDK de Flutter, Swift, Kotlin, React Native, Go y TypeScript
- [x] Migración de clientes, importaciones desde RevenueCat, ofertas e informes financieros de Apple (`v0.2.0-alpha.1`)
- [x] Métricas de suscripción, herramientas de atención al cliente y respuestas de consumo para reembolsos de Apple (en `main`, aún sin publicar)
- [ ] Superar los controles con tiendas reales de los tres proveedores y publicar `v0.1.0-rc.1`
- [ ] Publicar la versión estable `v0.1.0`
- [ ] Publicar paquetes versionados de los SDK ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Endpoint MCP para que los agentes de IA consulten y operen IAPStack con claves de privilegio mínimo ([#285](https://github.com/imariman/iapstack/issues/285))
- [ ] Reconocimiento de informes financieros de Google Play y Huawei
- [ ] Amazon Appstore y adaptadores para otras tiendas

Los elementos marcados están implementados en el repositorio; no implican que estén
preparados para producción.

<a id="contributing"></a>

## Cómo contribuir

Los informes de errores, las correcciones y la experiencia con las tiendas son bienvenidos.
Lee la [guía de contribución](CONTRIBUTING.md) antes de abrir un pull request, la
[guía de soporte](SUPPORT.md) para preguntas y la [política de seguridad](SECURITY.md) para
notificar una vulnerabilidad. La participación se rige por el
[Código de conducta](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Licencia

[Apache License 2.0](LICENSE)
