<p align="center">
  <img src="assets/iapstack-gopher.png" alt="Mascote Gopher do IAPStack" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Verificação de compras no app e direitos de acesso, open source e auto-hospedada.</strong>
  <br>
  Um único modelo controlado pelo backend para Apple App Store, Google Play e Huawei AppGallery.
  <br>
  Feito em Go. Independente de loja. Implante onde quiser.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/pt-br/"><strong>Site</strong></a>
  ·
  <a href="#quick-start"><strong>Início rápido</strong></a>
  ·
  <a href="#deploy"><strong>Implantação</strong></a>
  ·
  <a href="#documentation"><strong>Documentação</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Como contribuir</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="Painel de operações do IAPStack: saúde da rota de compra, atividade de verificação com um dia destacado, receita reconhecida das lojas e valor de testes em sandbox de um projeto de demonstração" width="100%">
  </picture>
</p>

<p align="center">
  <sub>O painel de operações, seguindo o tema do seu sistema (Sistema / Claro / Escuro no produto). Dados de demonstração sintéticos.</sub>
</p>

> [!NOTE]
> Esta é uma tradução do [README em inglês](README.md). Se houver divergência entre os dois, vale a versão em inglês. Os detalhes de desenvolvimento, a estrutura do repositório e a implantação passo a passo são mantidos apenas em inglês.

> [!IMPORTANT]
> O IAPStack é um software em pré-lançamento. As versões existentes `v0.1.0-rc1` e
> `v0.1.0-rc2` são prévias antigas, anteriores ao critério de liberação e às correções de
> segurança atuais; elas não têm suporte para uso em produção. A branch padrão continua em
> desenvolvimento, e o seu critério de ciclo de vida com lojas reais para os três
> provedores ainda não está concluído.

O IAPStack é um backend auto-hospedado com painel de controle que valida compras no app e
transforma transações das lojas em direitos de acesso duráveis para a sua aplicação. Ele
oferece às equipes um único modelo controlado pelo backend para todas as lojas móveis,
mantendo credenciais, dados de compra e decisões de acesso em uma infraestrutura que elas
controlam.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Verifique uma vez, em todas as lojas | Direitos de acesso, não recibos | Você é quem executa |
| --- | --- | --- |
| Verificação no servidor para Apple App Store, Google Play e Huawei AppGallery por trás de um modelo neutro em relação à loja. | Os apps perguntam o que um cliente pode acessar em vez de interpretar recibos brutos e estados das lojas. | Auto-hospedado por padrão: você controla a infraestrutura, as credenciais, os dados de compra e as decisões de acesso. |

<a id="current-scope"></a>

## Escopo atual

- Verificação no servidor de assinaturas e compras vitalícias não consumíveis
  da Apple App Store, do Google Play e da Huawei AppGallery
- Um modelo de transações e direitos de acesso neutro em relação à loja, apoiado no PostgreSQL
- Ingestão durável de notificações, reconciliação do ciclo de vida e webhooks de saída assinados
- Um painel de operações autenticado (temas Sistema / Claro / Escuro, uma barra lateral de
  projetos recolhível e gráficos de atividade e assinaturas inspecionáveis) com contratos
  HTTP/OpenAPI versionados
- Pacotes Flutter neutros em relação ao provedor e específicos de cada loja para os três provedores atuais
- Implantação compacta ou separada de API/worker com Docker e destinos de nuvem documentados

Entrega de consumíveis, migração de clientes e consolidação de aliases, operação hospedada
e lojas além das três acima estão fora do escopo da v0.1. Veja o
[escopo da v0.1](docs/v0.1-scope.md) para o limite completo.

<a id="sdk-roadmap"></a>

## Roadmap dos SDKs

O Flutter continua sendo o único cliente móvel no escopo da versão v0.1: um cliente HTTP
neutro em relação ao provedor, mais complementos para Apple, Google Play e Huawei. Código de
SDKs adicionais já está no repositório, seguindo o plano de expansão em
[#85](https://github.com/imariman/iapstack/issues/85). A presença desse código não amplia o
critério de liberação da v0.1 nem torna esses pacotes versões de produção.

| SDK | Implementado no repositório | Trabalho restante |
| --- | --- | --- |
| [Go para host confiável](sdk/go/README.md) | Sessões de cliente, consulta de direitos de acesso no servidor e tratamento de webhooks assinados | Primeira publicação versionada do módulo ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript para host confiável](sdk/typescript/README.md) | Sessões de cliente, consulta de direitos de acesso, webhooks assinados e arquivos ESM/de tipos instaláveis | Primeira publicação no npm ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | Cliente HTTP, complemento para StoreKit 2, exemplo executável com configuração do StoreKit, manifesto SwiftPM na raiz | Primeira tag de versão do SDK ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | Cliente HTTP, adaptadores para Play Billing/HMS, exemplo de host confiável e binários Maven/jars de código-fonte preparados | Primeira publicação no GitHub Maven ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | Cliente HTTP, ponte nativa para StoreKit/Play/Huawei, app de exemplo nativo e fontes dos complementos empacotadas | Primeira publicação no npm ([#141](https://github.com/imariman/iapstack/issues/141)) |

O [guia de publicação dos SDKs](docs/sdk-releases.md) documenta a instalação, as versões
de prévia independentes, as verificações de pacotes e a configuração dos registros. Código
neste repositório não implica que exista uma versão publicada em um registro.

O token bearer durável da aplicação fica em um backend de host confiável e nunca deve ser
incluído em um binário móvel ou em um bundle de navegador. Os clientes móveis usam sessões
de cliente de curta duração.

<a id="quick-start"></a>

## Início rápido

Você precisa do Docker com Compose e do OpenSSL. A partir de um clone deste repositório,
gere segredos apenas para uso local e suba o stack completo:

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

Guarde os valores gerados com segurança e reutilize-os com este volume do Compose.
Gerá-los novamente não rotaciona a senha existente do banco de dados nem as chaves de
proteção.

Depois, abra `http://127.0.0.1:8080/dashboard/` e conecte-se com a chave de administrador
de bootstrap. Crie e guarde uma chave de administrador durável, remova
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` do ambiente e recrie o contêiner da API.
O [guia de operações](docs/operations.md) explica o bootstrap, a rotação de chaves, o
desligamento, os backups, as atualizações e a base de segurança para produção.

<a id="deploy"></a>

## Implantação

Compare os requisitos de conta, as limitações dos planos gratuitos e os custos do stack
completo na [matriz de destinos de implantação](deploy/README.md) antes de criar recursos
em um provedor.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Destino | Formato | Guia |
| --- | --- | --- |
| Render | Um web service gratuito executando a API e o worker, um banco PostgreSQL gratuito para testes e Blueprints pagos compactos ou separados | [Guia do Render](deploy/render/README.md) |
| Heroku | API, worker e PostgreSQL gerenciado em dois dynos Basic sempre ativos; sem runtime gratuito | [Guia do Heroku](deploy/heroku/README.md) |
| DigitalOcean | App Platform com um worker e um job de migração, ou um Droplet de custo menor | [Guia do App Platform](deploy/digitalocean/README.md) · [Guia do Droplet](deploy/digitalocean/droplet.md) |
| Fly.io | Grupos de processos com Managed Postgres privado; apenas um teste gratuito curto | [Guia do Fly.io](deploy/fly/README.md) |
| Koyeb | Banco de dados gerenciado, API HTTPS e worker privado pela CLI oficial | [Guia do Koyeb](deploy/koyeb/README.md) |
| Railway | PostgreSQL e um serviço executando a API e o worker; cerca de USD 5 por mês no plano Hobby | [Guia do Railway](deploy/railway/README.md) |
| Coolify | O stack completo a partir de uma definição Docker Compose mantida no repositório, no seu próprio servidor | [Guia do Coolify](deploy/coolify/README.md) |
| VM única | A opção contínua mais barata: PostgreSQL, migrações, API e worker em uma única máquina Linux atrás de um Cloudflare Tunnel | [Guia de VM única](deploy/vps/README.md) |

### Opção de menor custo: uma única VM

Uma máquina Linux executa tudo a partir de
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml): PostgreSQL em uma rede Docker privada,
um contêiner de migração de execução única, um processo compacto com a API e o worker
durável, e um conector do Cloudflare Tunnel, de modo que a VM não precisa de portas de
entrada abertas. O worker precisa de um processo que fique em execução contínua, por isso
uma VM pequena sempre ativa se encaixa melhor do que plataformas que escalam a zero.

| Host | Custo típico | Indicação |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | Até 4 OCPUs Arm e 24 GB de RAM; stack do Resource Manager com um clique. |
| Google Cloud e2-micro (Free Tier, regiões dos EUA) | USD 0–4/mês | 1 GB de RAM para tráfego baixo; passo a passo guiado no Cloud Shell. |
| Hetzner Cloud CAX11 / CX22 | Cerca de EUR 4–5/mês | Cobrança previsível; instalado a partir de um único arquivo cloud-init. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | USD 6 / 12 por mês | IPv4 público incluído; mesmo arquivo cloud-init da Hetzner. |
| Um servidor que você já opera | USD 0 | Funciona atrás de NAT, mas as notificações das lojas exigem disponibilidade contínua. |

Você também precisa de um domínio com DNS no plano gratuito da Cloudflare. Confirme os
termos vigentes de cada provedor antes de criar recursos. Os passos de instalação, os
backups e as atualizações estão descritos na
[seção Deploy do README em inglês](README.md#deploy) e no
[guia de VM única](deploy/vps/README.md).

<a id="design-principles"></a>

## Princípios de design

**Auto-hospedado por padrão.** Você controla a infraestrutura, as credenciais, os dados de compra e as decisões de acesso.

**Lojas são adaptadores.** As APIs específicas de cada loja ficam isoladas por trás de contratos compartilhados; o domínio central não depende de um único marketplace.

**Direitos de acesso são a fronteira do produto.** As aplicações perguntam o que um cliente pode acessar em vez de interpretar recibos brutos ou estados das lojas.

**Implante em qualquer lugar.** O objetivo é uma implantação simples em contêineres, sem depender de uma plataforma de nuvem específica.

**Implementação independente.** A documentação oficial das lojas define o comportamento e os requisitos de segurança. Outros projetos open source podem ser estudados em busca de interoperabilidade e casos extremos, mas o código do IAPStack é implementado de forma independente.

<a id="documentation"></a>

## Documentação

A documentação é escrita em inglês.

- [Escopo e arquitetura da v0.1](docs/v0.1-scope.md)
- [Registros de decisões de arquitetura (ADRs)](docs/adr/README.md)
- [API HTTP v1 e contratos de notificação dos provedores](docs/api-v1.md)
- [Contrato OpenAPI v1 legível por máquina](contracts/openapi/v1.yaml)
- [Guia de operações e do painel](docs/operations.md)
- [Modelo de ameaças da v0.1 e checklist de segurança para liberação](docs/security.md)
- [Política de segurança e relato de vulnerabilidades](SECURITY.md)
- [Guia de contribuição](CONTRIBUTING.md)
- [Guia de suporte](SUPPORT.md)
- [SDK Flutter neutro em relação ao provedor](sdk/flutter/iapstack/README.md)
- [SDK nativo de iOS em Swift e complemento para StoreKit](sdk/ios/README.md)
- [SDK Go para host confiável](sdk/go/README.md)
- [SDK TypeScript para host confiável](sdk/typescript/README.md)

<a id="development"></a>

## Desenvolvimento

A compilação a partir do código-fonte, a configuração, os testes e a estrutura do
repositório estão descritos na [seção Development do README em inglês](README.md#development).
Leia o [guia de contribuição](CONTRIBUTING.md) antes de abrir um pull request.

<a id="roadmap"></a>

## Roadmap

- [x] Definir o domínio central, os contratos da API pública e o modelo de persistência
- [x] Implementar os adaptadores iniciais para Apple App Store, Google Play e Huawei AppGallery
- [x] Adicionar migrações do PostgreSQL e uma configuração Docker no formato de produção
- [x] Criar o SDK Flutter neutro em relação ao provedor, os três SDKs Flutter específicos de cada provedor e o painel
- [x] Oferecer suporte à reconciliação do ciclo de vida e a webhooks de saída assinados
- [x] Publicar um contrato de API v1 legível por máquina com verificações de compatibilidade dos clientes
- [x] Implementar os SDKs de Go, TypeScript, Swift, Kotlin e React Native
- [x] Oferecer suporte a migração de clientes, consolidação de aliases, importações do RevenueCat e importações de relatórios financeiros
- [x] Adicionar manifestos de implantação para Kubernetes
- [ ] Concluir os critérios de liberação do ciclo de vida para Apple App Store, Google Play e Huawei e publicar `v0.1.0-rc.1`
- [ ] Resolver o feedback sobre a versão candidata e publicar a versão estável `v0.1.0`
- [ ] Publicar pacotes versionados para host, Android, iOS e React Native ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Adicionar a Amazon Appstore e adaptadores para outras lojas

Os itens concluídos descrevem implementações no repositório, não a prontidão de uma versão
para produção.

<a id="license"></a>

## Licença

Licenciado sob a [Apache License 2.0](LICENSE).

Ao participar deste projeto, você concorda em seguir o
[Código de Conduta](CODE_OF_CONDUCT.md). Para os caminhos de contribuição e suporte, veja
[CONTRIBUTING.md](CONTRIBUTING.md) e [SUPPORT.md](SUPPORT.md).
