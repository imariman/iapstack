<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascote Gopher do IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verificação de compras no app e direitos de acesso, auto-hospedada.</strong>
  <br>
  Um único modelo controlado pelo backend para Apple App Store, Google Play e Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/pt-br/">Site</a>
  ·
  <a href="#quick-start">Início rápido</a>
  ·
  <a href="#deploy">Implantação</a>
  ·
  <a href="#documentation">Documentação</a>
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
    · <strong>Português (Brasil)</strong>
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
    <img src="assets/dashboard-light.png" alt="Painel de operações do IAPStack: saúde da rota de compra, atividade de verificação com um dia destacado, receita reconhecida das lojas e valor de testes em sandbox de um projeto de demonstração" width="100%">
  </picture>
</p>

> [!NOTE]
> Esta é uma tradução do [README em inglês](README.md). Se houver divergência entre os dois, vale a versão em inglês.

O IAPStack é um backend com painel de controle que verifica compras no app no servidor e
transforma transações das lojas em direitos de acesso duráveis. Seus apps fazem uma única
pergunta, *o que este cliente pode acessar?*, e a resposta é a mesma qualquer que seja a
loja de onde veio a compra. Credenciais, dados de compra e decisões de acesso ficam na
infraestrutura que você opera.

| Verifique uma vez, em todas as lojas | Direitos de acesso, não recibos | Você é quem executa |
| --- | --- | --- |
| Verificação no servidor para três lojas por trás de um único modelo neutro em relação à loja. As APIs das lojas ficam isoladas em adaptadores. | Os apps leem o estado de acesso em vez de interpretar recibos brutos e status específicos de cada loja. | Um único binário Go e PostgreSQL. Roda em Docker em qualquer nuvem ou em uma VM que você já tenha. |

> [!IMPORTANT]
> O IAPStack é um software em pré-lançamento e ainda não tem suporte para produção. As
> versões `v0.1.0-rc1` e `v0.1.0-rc2` são prévias antigas; a primeira versão candidata
> com suporte será lançada quando os critérios de ciclo de vida com lojas reais forem
> aprovados para os três provedores.

<a id="features"></a>

## Recursos

- **Verificação** de assinaturas e compras vitalícias não consumíveis da Apple App Store,
  do Google Play e da Huawei AppGallery
- **Acompanhamento do ciclo de vida** por meio da ingestão durável de notificações das
  lojas e de reconciliação agendada
- **Webhooks de saída assinados** para que o seu backend fique sabendo de cada mudança nos
  direitos de acesso
- **Painel de operações** para aplicações, clientes, filas e entregas de webhooks, com
  temas claro e escuro
- **API HTTP versionada** com um [contrato OpenAPI](contracts/openapi/v1.yaml) legível por
  máquina
- **SDKs de cliente** para Flutter, iOS, Android e React Native, além de SDKs Go e
  TypeScript para o seu próprio backend
- **Topologia flexível**: um único processo compacto ou serviços separados de API e worker

Entrega de consumíveis, operação hospedada e lojas além dessas três estão fora do escopo
da v0.1. Migração de clientes, importações do RevenueCat, ofertas (offerings) e relatórios
financeiros da Apple já existem na `main` e estão disponíveis como prévia na
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
mas não fazem parte do critério de liberação da v0.1. Veja o
[escopo da v0.1](docs/v0.1-scope.md) para o limite completo.

<a id="quick-start"></a>

## Início rápido

Você precisa do Docker com Compose e do OpenSSL. A partir de um clone deste repositório,
gere segredos locais e suba o stack:

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

Guarde esses valores: o volume do Compose depende deles, e gerar novos valores não
rotaciona a senha existente do banco de dados nem as chaves de proteção.

Abra `http://127.0.0.1:8080/dashboard/` e entre com a chave de administrador de
bootstrap. Crie uma chave de administrador durável, depois remova
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` e recrie o contêiner da API. O
[guia de operações](docs/operations.md) cobre o restante: rotação de chaves, backups,
atualizações e a base de segurança para produção.

<a id="deploy"></a>

## Implantação

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Todos os destinos executam a mesma imagem de contêiner com PostgreSQL, uma etapa de
migração, a API e um worker que precisa ficar em execução contínua. Compare os requisitos
de conta, os limites dos planos gratuitos e os custos do stack completo na
[matriz de destinos de implantação](deploy/README.md) antes de criar recursos.

| Destino | Formato | Guia |
| --- | --- | --- |
| **VM única** (Oracle, Google Cloud, Hetzner, DigitalOcean, seu próprio servidor) | A opção contínua mais barata: o stack completo em uma única máquina Linux atrás de um Cloudflare Tunnel, sem portas de entrada abertas. A partir de USD 0 por mês no Oracle Cloud Always Free. | [Guia de VM única](deploy/vps/README.md) |
| Render | Serviço compacto gratuito e PostgreSQL descartável para avaliação; Blueprints pagos compactos ou separados | [Render](deploy/render/README.md) |
| Railway | PostgreSQL e um único serviço de API/worker; normalmente dentro do plano Hobby de USD 5 | [Railway](deploy/railway/README.md) |
| Heroku | API, worker e PostgreSQL gerenciado em dois dynos Basic; sem plano gratuito | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, worker e job de migração a partir de um App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Grupos de processos com Managed Postgres privado | [Fly.io](deploy/fly/README.md) |
| Koyeb | Banco de dados gerenciado, API HTTPS e worker privado pela CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | O stack completo a partir da definição Compose do repositório, no seu servidor | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Manifestos simples para a API, o worker e o job de migração; traga o seu próprio PostgreSQL | [Kubernetes](deploy/k8s/README.md) |

Plataformas que escalam a zero e plataformas serverless não são uma boa opção: o worker
recebe as notificações das lojas, entrega webhooks e executa a reconciliação a cada
minuto.

<a id="sdks"></a>

## SDKs

Os apps móveis conversam com o IAPStack por meio de sessões de cliente de curta duração. A
chave durável da aplicação fica no seu backend e nunca deve ser incluída em um binário
móvel ou em um bundle de navegador.

| Plataforma | Pacote | Status |
| --- | --- | --- |
| Flutter | [Cliente neutro em relação ao provedor](sdk/flutter/iapstack/README.md) com complementos para [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) e [Huawei](sdk/flutter/iapstack_huawei/README.md) | Escopo da versão v0.1 |
| iOS (Swift) | [Cliente HTTP e complemento para StoreKit 2](sdk/ios/README.md) | Implementado, ainda não publicado |
| Android (Kotlin) | [Cliente HTTP com adaptadores para Play Billing e HMS](sdk/android/README.md) | Implementado, ainda não publicado |
| React Native | [Cliente HTTP e ponte nativa para StoreKit/Play/Huawei](sdk/react-native/README.md) | Implementado, ainda não publicado |
| Go (backend) | [Sessões de cliente, consulta de direitos de acesso, verificação de webhooks](sdk/go/README.md) | Implementado, ainda não publicado |
| TypeScript (backend) | [Sessões de cliente, consulta de direitos de acesso, verificação de webhooks](sdk/typescript/README.md) | Implementado, ainda não publicado |

A publicação nos registros é acompanhada em
[#141](https://github.com/imariman/iapstack/issues/141). O
[guia de publicação dos SDKs](docs/sdk-releases.md) cobre a instalação a partir do
código-fonte, o versionamento e a automação de releases.

<a id="documentation"></a>

## Documentação

A documentação é escrita em inglês.

| Guia | O que cobre |
| --- | --- |
| [Operações](docs/operations.md) | Segredos, configuração, painel, configuração das notificações das lojas, backups, atualizações, releases |
| [API HTTP v1](docs/api-v1.md) | Endpoints, credenciais dos provedores, notificações, verificação de webhooks |
| [Escopo e arquitetura da v0.1](docs/v0.1-scope.md) | Contexto do sistema, invariantes centrais, fatias por provedor |
| [Segurança](docs/security.md) | Modelo de ameaças e checklist de segurança para liberação |
| [Decisões de arquitetura](docs/adr/README.md) | Por que as principais fronteiras existem |
| [Destinos de implantação](deploy/README.md) | Comparação entre provedores e guias por provedor |
| [Runbook de liberação](docs/releases/v0.1.0-rc.1.md) | O critério de liberação com lojas reais para os três provedores |

<a id="development"></a>

## Desenvolvimento

Use o toolchain Go declarado em `go.mod`. Suba o PostgreSQL, aplique as migrações e
execute a API e o worker em um único processo:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

O servidor também precisa das chaves de proteção do [início rápido](#quick-start); todas
as configurações estão listadas na
[referência de configuração](docs/operations.md#configuration-reference). A API escuta
em `:8080`, serve `/healthz` e `/readyz` e embute o painel em `/dashboard/`.

Execute as verificações do servidor e o critério de liberação de ponta a ponta:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

O [guia de contribuição](CONTRIBUTING.md) traz as verificações dos SDKs, a configuração
dos testes de integração, uma visão geral da arquitetura e as convenções de código.

<a id="repository-layout"></a>

### Estrutura do repositório

```text
.
├── cmd/iapstack/       # Pontos de entrada da API, do worker e das migrações
├── internal/
│   ├── core/           # Clientes, transações, produtos e direitos de acesso
│   ├── verification/   # Verificação de compras neutra em relação ao provedor
│   ├── stores/         # Adaptadores para Apple, Google Play e Huawei
│   ├── credentials/    # Credenciais protegidas das aplicações
│   ├── protection/     # Porta de proteção de dados sensíveis
│   ├── persistence/    # Repositórios PostgreSQL
│   └── platform/       # Implementações de configuração, logging e keyring
├── dashboard/          # Painel de operações embutido
├── contracts/          # Contratos OpenAPI e de webhooks
├── sdk/                # SDKs Flutter, iOS, Android, React Native, Go e TypeScript
├── deploy/             # Definições para Compose, nuvem, VM e Kubernetes
└── docs/               # Guias, ADRs e runbooks de liberação
```

<a id="roadmap"></a>

## Roadmap

- [x] Domínio neutro em relação à loja, persistência em PostgreSQL e contrato OpenAPI versionado
- [x] Adaptadores para Apple App Store, Google Play e Huawei AppGallery
- [x] Reconciliação do ciclo de vida e webhooks de saída assinados
- [x] Painel de operações e definições de implantação para nuvem, VM e Kubernetes
- [x] SDKs Flutter, Swift, Kotlin, React Native, Go e TypeScript
- [x] Migração de clientes, importações do RevenueCat, ofertas e relatórios financeiros da Apple (`v0.2.0-alpha.1`)
- [x] Métricas de assinatura, ferramentas de suporte ao cliente e respostas de consumo para reembolsos da Apple (na `main`, ainda não lançadas)
- [ ] Passar nos critérios com lojas reais para os três provedores e publicar `v0.1.0-rc.1`
- [ ] Publicar a versão estável `v0.1.0`
- [ ] Publicar pacotes versionados dos SDKs ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Endpoint MCP para que agentes de IA consultem e operem o IAPStack com chaves de privilégio mínimo ([#285](https://github.com/imariman/iapstack/issues/285))
- [ ] Reconhecimento de relatórios financeiros do Google Play e da Huawei
- [ ] Amazon Appstore e adaptadores para outras lojas

Os itens marcados estão implementados no repositório; isso não significa que estejam
prontos para produção.

<a id="contributing"></a>

## Como contribuir

Relatos de bugs, correções e conhecimento sobre as lojas são bem-vindos. Leia o
[guia de contribuição](CONTRIBUTING.md) antes de abrir um pull request, o
[guia de suporte](SUPPORT.md) para tirar dúvidas e a [política de segurança](SECURITY.md)
para relatar uma vulnerabilidade. A participação é regida pelo
[Código de Conduta](CODE_OF_CONDUCT.md).

<a id="license"></a>

## Licença

[Apache License 2.0](LICENSE)
