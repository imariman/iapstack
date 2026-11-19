<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascota Gopher de IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verificación de compras dentro de la app y derechos de acceso, de código abierto y autoalojada.</strong>
  <br>
  Un único modelo gestionado por el backend para Apple App Store, Google Play y Huawei AppGallery.
  <br>
  Escrito en Go. Independiente de las tiendas. Despliégalo donde quieras.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/es/"><strong>Sitio web</strong></a>
  ·
  <a href="#quick-start"><strong>Inicio rápido</strong></a>
  ·
  <a href="#deploy"><strong>Despliegue</strong></a>
  ·
  <a href="#documentation"><strong>Documentación</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Cómo contribuir</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="Panel de operaciones de IAPStack: estado de la ruta de compra, actividad de verificación con un día resaltado, ingresos reconocidos de las tiendas y valor de pruebas en sandbox de un proyecto de demostración" width="100%">
  </picture>
</p>

<p align="center">
  <sub>El panel de operaciones, siguiendo el tema de tu sistema (Sistema / Claro / Oscuro en el producto). Se muestran datos de demostración sintéticos.</sub>
</p>

> [!NOTE]
> Esta es una traducción del [README en inglés](README.md). Si ambos difieren, prevalece la versión en inglés. Los detalles sobre desarrollo, la estructura del repositorio y el despliegue paso a paso solo se mantienen en inglés.

> [!IMPORTANT]
> IAPStack es software en fase de prelanzamiento. Las versiones existentes `v0.1.0-rc1` y
> `v0.1.0-rc2` son versiones preliminares antiguas, anteriores al control de publicación
> y a las correcciones de seguridad actuales; no tienen soporte para uso en producción.
> La rama principal sigue en desarrollo y su control de ciclo de vida con tiendas reales
> para los tres proveedores aún no está completo.

IAPStack es un backend autoalojado con panel de control que valida compras dentro de la app
y convierte las transacciones de las tiendas en derechos de acceso duraderos para tu
aplicación. Ofrece a los equipos un único modelo gestionado por el backend para todas las
tiendas móviles, mientras las credenciales, los datos de compra y las decisiones de acceso
permanecen en infraestructura que ellos controlan.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Verifica una vez, en todas partes | Derechos de acceso, no recibos | Tú lo ejecutas |
| --- | --- | --- |
| Verificación en el servidor para Apple App Store, Google Play y Huawei AppGallery detrás de un modelo neutral respecto a la tienda. | Las apps preguntan a qué puede acceder un cliente en lugar de interpretar recibos sin procesar y estados de las tiendas. | Autoalojado por defecto: tú controlas la infraestructura, las credenciales, los datos de compra y las decisiones de acceso. |

<a id="current-scope"></a>

## Alcance actual

- Verificación en el servidor de suscripciones y compras vitalicias no consumibles
  de Apple App Store, Google Play y Huawei AppGallery
- Un modelo de transacciones y derechos de acceso neutral respecto a la tienda, respaldado por PostgreSQL
- Ingesta duradera de notificaciones, conciliación del ciclo de vida y webhooks salientes firmados
- Un panel de operaciones con autenticación (temas Sistema / Claro / Oscuro, una barra
  lateral de proyectos plegable y gráficos de actividad y suscripciones inspeccionables)
  con contratos HTTP/OpenAPI versionados
- Paquetes de Flutter neutrales respecto al proveedor y específicos de cada tienda para los tres proveedores actuales
- Despliegue de API/worker compacto o separado con Docker y destinos en la nube documentados

El suministro de consumibles, la migración de clientes y la consolidación de alias, la
operación alojada y las tiendas distintas de las tres anteriores quedan fuera del alcance
de la v0.1. Consulta el [alcance de la v0.1](docs/v0.1-scope.md) para ver el límite completo.

<a id="sdk-roadmap"></a>

## Hoja de ruta de los SDK

Flutter sigue siendo el único cliente móvil dentro del alcance de la versión v0.1: un
cliente HTTP neutral respecto al proveedor más complementos para Apple, Google Play y
Huawei. El repositorio ya contiene código de SDK adicional, conforme al plan de expansión de
[#85](https://github.com/imariman/iapstack/issues/85). Su presencia no amplía el control de
publicación de la v0.1 ni convierte estos paquetes en versiones de producción.

| SDK | Implementado en el repositorio | Trabajo pendiente |
| --- | --- | --- |
| [Go para host de confianza](sdk/go/README.md) | Sesiones de cliente, consulta de derechos de acceso en el servidor y gestión de webhooks firmados | Primera publicación versionada del módulo ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript para host de confianza](sdk/typescript/README.md) | Sesiones de cliente, consulta de derechos de acceso, webhooks firmados y archivos ESM/de tipos instalables | Primera publicación en npm ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | Cliente HTTP, complemento para StoreKit 2, ejemplo ejecutable con configuración de StoreKit, manifiesto SwiftPM en la raíz | Primera etiqueta de versión del SDK ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | Cliente HTTP, adaptadores para Play Billing/HMS, ejemplo de host de confianza y binarios Maven/jars de código fuente preparados | Primera publicación en GitHub Maven ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | Cliente HTTP, puente nativo para StoreKit/Play/Huawei, app de ejemplo nativa y fuentes de complementos empaquetadas | Primera publicación en npm ([#141](https://github.com/imariman/iapstack/issues/141)) |

La [guía de publicación de los SDK](docs/sdk-releases.md) documenta la instalación, las
versiones preliminares independientes, las comprobaciones de paquetes y la configuración de
los registros. Que el código esté en este repositorio no implica que exista una versión
publicada en un registro.

El token bearer duradero de la aplicación permanece en un backend de host de confianza y
nunca debe incluirse en un binario móvil ni en un bundle de navegador. Los clientes móviles
usan sesiones de cliente de corta duración.

<a id="quick-start"></a>

## Inicio rápido

Necesitas Docker con Compose y OpenSSL. Desde un clon de este repositorio, genera secretos
solo para uso local e inicia el stack completo:

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

Guarda los valores generados de forma segura y reutilízalos con este volumen de Compose.
Volver a generarlos no rota la contraseña de la base de datos ni las claves de protección
existentes.

Después abre `http://127.0.0.1:8080/dashboard/` y conéctate con la clave de administrador
de arranque. Crea y guarda una clave de administrador duradera, elimina
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` del entorno y vuelve a crear el contenedor de la API.
La [guía de operaciones](docs/operations.md) explica el arranque, la rotación de claves, el
apagado, las copias de seguridad, las actualizaciones y la base de seguridad para producción.

<a id="deploy"></a>

## Despliegue

Compara los requisitos de cuenta, las limitaciones de los planes gratuitos y el coste del
stack completo en la [matriz de destinos de despliegue](deploy/README.md) antes de crear
recursos en un proveedor.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Destino | Configuración | Guía |
| --- | --- | --- |
| Render | Un servicio web gratuito que ejecuta la API y el worker, una base de datos PostgreSQL gratuita para pruebas y Blueprints de pago compactos o separados | [Guía de Render](deploy/render/README.md) |
| Heroku | API, worker y PostgreSQL gestionado en dos dynos Basic siempre activos; sin entorno de ejecución gratuito | [Guía de Heroku](deploy/heroku/README.md) |
| DigitalOcean | App Platform con un worker y un job de migración, o un Droplet de menor coste | [Guía de App Platform](deploy/digitalocean/README.md) · [Guía de Droplet](deploy/digitalocean/droplet.md) |
| Fly.io | Grupos de procesos con Managed Postgres privado; solo una prueba gratuita breve | [Guía de Fly.io](deploy/fly/README.md) |
| Koyeb | Base de datos gestionada, API HTTPS y worker privado mediante la CLI oficial | [Guía de Koyeb](deploy/koyeb/README.md) |
| Railway | PostgreSQL y un servicio que ejecuta la API y el worker; unos USD 5 al mes en el plan Hobby | [Guía de Railway](deploy/railway/README.md) |
| Coolify | El stack completo a partir de una definición de Docker Compose incluida en el repositorio, en tu propio servidor | [Guía de Coolify](deploy/coolify/README.md) |
| VM única | La opción continua más barata: PostgreSQL, migraciones, API y worker en una sola máquina Linux detrás de un Cloudflare Tunnel | [Guía de VM única](deploy/vps/README.md) |

### Opción de menor coste: una sola VM

Una máquina Linux lo ejecuta todo a partir de
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml): PostgreSQL en una red Docker privada,
un contenedor de migración de un solo uso, un proceso compacto que contiene la API y el
worker duradero, y un conector de Cloudflare Tunnel, de modo que la VM no necesita puertos
de entrada abiertos. El worker necesita un proceso que se mantenga en ejecución, por eso una
VM pequeña siempre activa encaja mejor que las plataformas que escalan a cero.

| Host | Coste típico | Adecuación |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | Hasta 4 OCPU Arm y 24 GB de RAM; stack de Resource Manager con un clic. |
| Google Cloud e2-micro (Free Tier, regiones de EE. UU.) | USD 0–4/mes | 1 GB de RAM para poco tráfico; recorrido guiado en Cloud Shell. |
| Hetzner Cloud CAX11 / CX22 | Unos EUR 4–5/mes | Facturación predecible; se instala con un único archivo cloud-init. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | USD 6 / 12 al mes | IPv4 pública incluida; el mismo archivo cloud-init que Hetzner. |
| Un servidor que ya administras | USD 0 | Funciona detrás de NAT, pero las notificaciones de las tiendas requieren disponibilidad continua. |

También necesitas un dominio cuyo DNS esté en el plan gratuito de Cloudflare. Confirma las
condiciones vigentes de cada proveedor antes de crear recursos. Los pasos de instalación,
las copias de seguridad y las actualizaciones se describen en la
[sección Deploy del README en inglés](README.md#deploy) y en la
[guía de VM única](deploy/vps/README.md).

<a id="design-principles"></a>

## Principios de diseño

**Autoalojado por defecto.** Tú controlas la infraestructura, las credenciales, los datos de compra y las decisiones de acceso.

**Las tiendas son adaptadores.** Las API específicas de cada tienda permanecen aisladas detrás de contratos compartidos; el dominio central no depende de un único marketplace.

**Los derechos de acceso son el límite del producto.** Las aplicaciones preguntan a qué puede acceder un cliente en lugar de interpretar recibos sin procesar o estados de las tiendas.

**Despliega donde quieras.** El objetivo es un despliegue sencillo en contenedores, sin depender de una plataforma en la nube concreta.

**Implementación independiente.** La documentación oficial de las tiendas define el comportamiento y los requisitos de seguridad. Otros proyectos de código abierto pueden estudiarse para la interoperabilidad y los casos límite, pero el código de IAPStack se implementa de forma independiente.

<a id="documentation"></a>

## Documentación

La documentación está escrita en inglés.

- [Alcance y arquitectura de la v0.1](docs/v0.1-scope.md)
- [Registros de decisiones de arquitectura (ADR)](docs/adr/README.md)
- [API HTTP v1 y contratos de notificaciones de proveedores](docs/api-v1.md)
- [Contrato OpenAPI v1 legible por máquinas](contracts/openapi/v1.yaml)
- [Guía de operaciones y del panel](docs/operations.md)
- [Modelo de amenazas de la v0.1 y lista de comprobación de seguridad para la publicación](docs/security.md)
- [Política de seguridad y notificación de vulnerabilidades](SECURITY.md)
- [Guía de contribución](CONTRIBUTING.md)
- [Guía de soporte](SUPPORT.md)
- [SDK de Flutter neutral respecto al proveedor](sdk/flutter/iapstack/README.md)
- [SDK nativo de iOS en Swift y complemento para StoreKit](sdk/ios/README.md)
- [SDK de Go para host de confianza](sdk/go/README.md)
- [SDK de TypeScript para host de confianza](sdk/typescript/README.md)

<a id="development"></a>

## Desarrollo

La compilación desde el código fuente, la configuración, las pruebas y la estructura del
repositorio se describen en la [sección Development del README en inglés](README.md#development).
Lee la [guía de contribución](CONTRIBUTING.md) antes de abrir un pull request.

<a id="roadmap"></a>

## Hoja de ruta

- [x] Definir el dominio central, los contratos de la API pública y el modelo de persistencia
- [x] Implementar los adaptadores iniciales de Apple App Store, Google Play y Huawei AppGallery
- [x] Añadir migraciones de PostgreSQL y una configuración de Docker orientada a producción
- [x] Crear el SDK de Flutter neutral respecto al proveedor, los tres SDK de Flutter específicos de cada proveedor y el panel
- [x] Admitir la conciliación del ciclo de vida y los webhooks salientes firmados
- [x] Publicar un contrato de API v1 legible por máquinas con comprobaciones de compatibilidad de clientes
- [x] Implementar los SDK de Go, TypeScript, Swift, Kotlin y React Native
- [x] Admitir la migración de clientes, la consolidación de alias, las importaciones desde RevenueCat y las importaciones de informes financieros
- [x] Añadir manifiestos de despliegue para Kubernetes
- [ ] Completar los controles de publicación del ciclo de vida de Apple App Store, Google Play y Huawei y publicar `v0.1.0-rc.1`
- [ ] Resolver los comentarios sobre la versión candidata y publicar la versión estable `v0.1.0`
- [ ] Publicar paquetes versionados para host, Android, iOS y React Native ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Añadir Amazon Appstore y adaptadores para otras tiendas

Los elementos completados describen implementaciones en el repositorio, no la preparación de
una versión para producción.

<a id="license"></a>

## Licencia

Publicado bajo la [Apache License 2.0](LICENSE).

Al participar en este proyecto, aceptas seguir el
[Código de conducta](CODE_OF_CONDUCT.md). Para las vías de contribución y soporte, consulta
[CONTRIBUTING.md](CONTRIBUTING.md) y [SUPPORT.md](SUPPORT.md).
