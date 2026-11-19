<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack Gopher maskotu" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Kendi sunucunuzda barındırılan uygulama içi satın alma doğrulama ve erişim hakları.</strong>
  <br>
  Apple App Store, Google Play ve Huawei AppGallery için backend'in sahip olduğu tek bir model.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/tr/">Web sitesi</a>
  ·
  <a href="#quick-start">Hızlı başlangıç</a>
  ·
  <a href="#deploy">Dağıtım</a>
  ·
  <a href="#documentation">Dokümantasyon</a>
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
    · <a href="README.pt-BR.md">Português (Brasil)</a>
    · <strong>Türkçe</strong>
    · <a href="README.ru.md">Русский</a>
    · <a href="README.ja.md">日本語</a>
    · <a href="README.ko.md">한국어</a>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack operasyon paneli: bir demo proje için satın alma rotasının durumu, üzerine gelinen bir günle doğrulama etkinliği, tanınan mağaza geliri ve sandbox test değeri" width="100%">
  </picture>
</p>

> [!NOTE]
> Bu belge, [İngilizce README](README.md) dosyasının çevirisidir. İkisi arasında fark olursa İngilizce sürüm esas alınır.

IAPStack, uygulama içi satın almaları sunucuda doğrulayan ve mağaza işlemlerini kalıcı
erişim haklarına dönüştüren bir backend ve kontrol panelidir. Uygulamalarınız tek bir soru
sorar: *bu müşteri neye erişebilir?* Satın alma hangi mağazadan gelirse gelsin cevap
aynıdır. Kimlik bilgileri, satın alma verileri ve erişim kararları sizin işlettiğiniz
altyapıda kalır.

| Bir kez doğrulayın, her yerde | Makbuzlar değil, erişim hakları | Çalıştırması sizde |
| --- | --- | --- |
| Üç mağaza için, mağazadan bağımsız tek bir modelin arkasında sunucu tarafı doğrulama. Mağaza API'leri adaptörler içinde yalıtılmış kalır. | Uygulamalar ham makbuzları ve mağazaya özgü durumları yorumlamak yerine erişim durumunu okur. | Tek bir Go ikili dosyası ve PostgreSQL. Herhangi bir bulutta ya da zaten sahip olduğunuz bir VM'de Docker ile çalışır. |

> [!IMPORTANT]
> IAPStack bir ön sürüm yazılımıdır ve henüz üretim kullanımı için desteklenmez.
> `v0.1.0-rc1` ve `v0.1.0-rc2` sürümleri eski önizlemelerdir; desteklenen ilk aday sürüm,
> gerçek mağaza yaşam döngüsü kapıları üç sağlayıcı için de geçildiğinde yayımlanacak.

<a id="features"></a>

## Özellikler

- Apple App Store, Google Play ve Huawei AppGallery'deki abonelikler ve tüketilemeyen
  ömür boyu satın almalar için **doğrulama**
- Kalıcı mağaza bildirimi alımı ve zamanlanmış mutabakat ile **yaşam döngüsü takibi**
- Backend'inizin her erişim hakkı değişikliğinden haberdar olması için **imzalı giden webhook'lar**
- Uygulamalar, müşteriler, kuyruklar ve webhook teslimatları için açık ve koyu temalı
  **operasyon paneli**
- Makine tarafından okunabilir bir [OpenAPI sözleşmesiyle](contracts/openapi/v1.yaml)
  **sürümlü HTTP API**
- Flutter, iOS, Android ve React Native için **istemci SDK'ları**; ayrıca kendi
  backend'iniz için Go ve TypeScript SDK'ları
- **Esnek topoloji**: tek bir kompakt süreç ya da ayrı API ve worker servisleri

Tüketilebilir ürün teslimi, barındırılan işletim ve bu üçü dışındaki mağazalar v0.1
kapsamı dışındadır. Müşteri taşıma, RevenueCat içe aktarma, teklifler (offerings) ve Apple
finansal raporları `main` dalında zaten mevcuttur ve
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1)
ile önizleme olarak sunulur; ancak v0.1 sürüm kapısının parçası değildir. Sınırın tamamı
için [v0.1 kapsamına](docs/v0.1-scope.md) bakın.

<a id="quick-start"></a>

## Hızlı başlangıç

Compose destekli Docker'a ve OpenSSL'e ihtiyacınız var. Bu deponun bir klonunda yerel
gizli anahtarlar oluşturun ve yığını başlatın:

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

Bu değerleri saklayın: Compose birimi bunlara bağlıdır ve yenilerini oluşturmak mevcut
veritabanı parolasını veya koruma anahtarlarını döndürmez.

`http://127.0.0.1:8080/dashboard/` adresini açın ve önyükleme (bootstrap) yönetici
anahtarıyla giriş yapın. Kalıcı bir yönetici anahtarı oluşturun, ardından
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` değişkenini kaldırın ve API container'ını yeniden
oluşturun. Gerisini [operasyon kılavuzu](docs/operations.md) anlatır: anahtar döndürme,
yedeklemeler, yükseltmeler ve üretim güvenliği temel çizgisi.

<a id="deploy"></a>

## Dağıtım

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Her hedef aynı container imajını çalıştırır: PostgreSQL, bir migration adımı, API ve
sürekli çalışması gereken bir worker. Kaynakları oluşturmadan önce hesap gereksinimlerini,
ücretsiz katman sınırlarını ve yığının tamamının maliyetlerini
[dağıtım hedefleri tablosunda](deploy/README.md) karşılaştırın.

| Hedef | Yapı | Kılavuz |
| --- | --- | --- |
| **Tek VM** (Oracle, Google Cloud, Hetzner, DigitalOcean, kendi sunucunuz) | Sürekli çalışan en ucuz seçenek: yığının tamamı, bir Cloudflare Tunnel arkasında tek bir Linux makinesinde, gelen bağlantılara açık port olmadan. Oracle Cloud Always Free'de ayda USD 0'dan başlar. | [Tek VM kılavuzu](deploy/vps/README.md) |
| Render | Değerlendirme için ücretsiz kompakt servis ve geçici PostgreSQL; ücretli kompakt ya da ayrık Blueprint'ler | [Render](deploy/render/README.md) |
| Railway | PostgreSQL ve tek bir API/worker servisi; genellikle USD 5'lik Hobby planı içinde kalır | [Railway](deploy/railway/README.md) |
| Heroku | İki Basic dyno üzerinde API, worker ve yönetilen PostgreSQL; ücretsiz katman yok | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | Bir App Spec'ten API, worker ve migration işi | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Özel Managed Postgres ile süreç grupları | [Fly.io](deploy/fly/README.md) |
| Koyeb | CLI üzerinden yönetilen veritabanı, HTTPS API ve özel worker | [Koyeb](deploy/koyeb/README.md) |
| Coolify | Deponun Compose tanımından, kendi sunucunuzda yığının tamamı | [Coolify](deploy/coolify/README.md) |
| Kubernetes | API, worker ve migration işi için düz manifestler; PostgreSQL'i kendiniz sağlarsınız | [Kubernetes](deploy/k8s/README.md) |

Sıfıra ölçeklenen ve sunucusuz platformlar pek uygun değildir: worker mağaza
bildirimlerini alır, webhook'ları teslim eder ve her dakika mutabakat çalıştırır.

<a id="sdks"></a>

## SDK'lar

Mobil uygulamalar IAPStack ile kısa ömürlü müşteri oturumları üzerinden konuşur. Kalıcı
uygulama anahtarı backend'inizde kalır ve hiçbir zaman bir mobil ikili dosyaya veya
tarayıcı paketine konmamalıdır.

| Platform | Paket | Durum |
| --- | --- | --- |
| Flutter | [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md) ve [Huawei](sdk/flutter/iapstack_huawei/README.md) yardımcı paketleriyle [sağlayıcıdan bağımsız istemci](sdk/flutter/iapstack/README.md) | v0.1 sürüm kapsamında |
| iOS (Swift) | [HTTP istemcisi ve StoreKit 2 yardımcı paketi](sdk/ios/README.md) | Uygulandı, henüz yayımlanmadı |
| Android (Kotlin) | [Play Billing ve HMS adaptörlü HTTP istemcisi](sdk/android/README.md) | Uygulandı, henüz yayımlanmadı |
| React Native | [HTTP istemcisi ve yerel StoreKit/Play/Huawei köprüsü](sdk/react-native/README.md) | Uygulandı, henüz yayımlanmadı |
| Go (backend) | [Müşteri oturumları, erişim hakkı sorgulama, webhook doğrulama](sdk/go/README.md) | Uygulandı, henüz yayımlanmadı |
| TypeScript (backend) | [Müşteri oturumları, erişim hakkı sorgulama, webhook doğrulama](sdk/typescript/README.md) | Uygulandı, henüz yayımlanmadı |

Kayıt depolarında yayın süreci [#141](https://github.com/imariman/iapstack/issues/141)
numaralı issue'da takip ediliyor. [SDK sürüm kılavuzu](docs/sdk-releases.md) kaynaktan
kurulumu, sürümlemeyi ve sürüm otomasyonunu açıklar.

<a id="documentation"></a>

## Dokümantasyon

Dokümantasyon İngilizce yazılmıştır.

| Kılavuz | Kapsamı |
| --- | --- |
| [Operasyon](docs/operations.md) | Gizli anahtarlar, yapılandırma, panel, mağaza bildirimi kurulumu, yedeklemeler, yükseltmeler, sürümler |
| [HTTP API v1](docs/api-v1.md) | Uç noktalar, sağlayıcı kimlik bilgileri, bildirimler, webhook doğrulama |
| [v0.1 kapsamı ve mimarisi](docs/v0.1-scope.md) | Sistem bağlamı, temel değişmezler, sağlayıcı dilimleri |
| [Güvenlik](docs/security.md) | Tehdit modeli ve sürüm güvenliği kontrol listesi |
| [Mimari kararlar](docs/adr/README.md) | Ana sınırların neden var olduğu |
| [Dağıtım hedefleri](deploy/README.md) | Sağlayıcı karşılaştırması ve sağlayıcıya özel kılavuzlar |
| [Sürüm runbook'u](docs/releases/v0.1.0-rc.1.md) | Üç sağlayıcılı gerçek mağaza sürüm kapısı |

<a id="development"></a>

## Geliştirme

`go.mod` dosyasında belirtilen Go araç zincirini kullanın. PostgreSQL'i başlatın,
migration'ları uygulayın ve API ile worker'ı tek bir süreçte çalıştırın:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

Sunucunun ayrıca [hızlı başlangıçtaki](#quick-start) koruma anahtarlarına ihtiyacı vardır;
tüm ayarlar [yapılandırma referansında](docs/operations.md#configuration-reference)
listelenmiştir. API `:8080` portunu dinler, `/healthz` ve `/readyz` uç noktalarını sunar
ve paneli `/dashboard/` altında gömülü olarak barındırır.

Sunucu kontrollerini ve uçtan uca sürüm kapısını çalıştırın:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

[Katkı kılavuzu](CONTRIBUTING.md); SDK kontrollerini, entegrasyon testi kurulumunu, bir
mimari genel bakışı ve kod kurallarını içerir.

<a id="repository-layout"></a>

### Depo yapısı

```text
.
├── cmd/iapstack/       # API, worker ve migration giriş noktaları
├── internal/
│   ├── core/           # Müşteriler, işlemler, ürünler ve erişim hakları
│   ├── verification/   # Sağlayıcıdan bağımsız satın alma doğrulama
│   ├── stores/         # Apple, Google Play ve Huawei adaptörleri
│   ├── credentials/    # Korunan uygulama kimlik bilgileri
│   ├── protection/     # Hassas veri koruma portu
│   ├── persistence/    # PostgreSQL repository'leri
│   └── platform/       # Yapılandırma, loglama ve anahtarlık uygulamaları
├── dashboard/          # Gömülü operasyon paneli
├── contracts/          # OpenAPI ve webhook sözleşmeleri
├── sdk/                # Flutter, iOS, Android, React Native, Go ve TypeScript SDK'ları
├── deploy/             # Compose, bulut, VM ve Kubernetes tanımları
└── docs/               # Kılavuzlar, ADR'ler ve sürüm runbook'ları
```

<a id="roadmap"></a>

## Yol haritası

- [x] Mağazadan bağımsız alan modeli, PostgreSQL kalıcılığı ve sürümlü OpenAPI sözleşmesi
- [x] Apple App Store, Google Play ve Huawei AppGallery adaptörleri
- [x] Yaşam döngüsü mutabakatı ve imzalı giden webhook'lar
- [x] Operasyon paneli ve bulut, VM ve Kubernetes için dağıtım tanımları
- [x] Flutter, Swift, Kotlin, React Native, Go ve TypeScript SDK'ları
- [x] Müşteri taşıma, RevenueCat içe aktarma, teklifler ve Apple finansal raporları (`v0.2.0-alpha.1`)
- [ ] Üç sağlayıcılı gerçek mağaza kapılarını geçmek ve `v0.1.0-rc.1` sürümünü yayımlamak
- [ ] Kararlı `v0.1.0` sürümünü yayımlamak
- [ ] Sürümlü SDK paketlerini yayımlamak ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Google Play ve Huawei finansal raporlarında gelir tanıma
- [ ] Amazon Appstore ve ek mağaza adaptörleri

İşaretli maddeler depoda uygulanmış olanları ifade eder; üretime hazır olunduğu anlamına
gelmez.

<a id="contributing"></a>

## Katkıda bulunma

Hata raporları, düzeltmeler ve mağaza uzmanlığı memnuniyetle karşılanır. Bir pull request
açmadan önce [katkı kılavuzunu](CONTRIBUTING.md), sorularınız için
[destek kılavuzunu](SUPPORT.md), bir güvenlik açığı bildirmek için de
[güvenlik politikasını](SECURITY.md) okuyun. Katılım,
[Davranış Kuralları](CODE_OF_CONDUCT.md) çerçevesinde yürütülür.

<a id="license"></a>

## Lisans

[Apache License 2.0](LICENSE)
