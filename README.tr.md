<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack Gopher maskotu" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Açık kaynaklı, kendi sunucunuzda barındırılan uygulama içi satın alma doğrulama ve erişim hakları.</strong>
  <br>
  Apple App Store, Google Play ve Huawei AppGallery için backend'in sahip olduğu tek bir model.
  <br>
  Go ile geliştirildi. Mağazadan bağımsız. Her yere kurulabilir.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/tr/"><strong>Web sitesi</strong></a>
  ·
  <a href="#quick-start"><strong>Hızlı başlangıç</strong></a>
  ·
  <a href="#deploy"><strong>Dağıtım</strong></a>
  ·
  <a href="#documentation"><strong>Dokümantasyon</strong></a>
  ·
  <a href="contracts/openapi/v1.yaml"><strong>OpenAPI</strong></a>
  ·
  <a href="CONTRIBUTING.md"><strong>Katkıda bulunma</strong></a>
</p>

<p align="center">
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
    <img src="assets/dashboard-light.png" alt="IAPStack operasyon paneli: bir demo proje için satın alma rotasının durumu, üzerine gelinen bir günle doğrulama etkinliği, tanınan mağaza geliri ve sandbox test değeri" width="100%">
  </picture>
</p>

<p align="center">
  <sub>Sistem temanızı izleyen operasyon paneli (üründe Sistem / Açık / Koyu). Sentetik demo verileri gösterilmektedir.</sub>
</p>

> [!NOTE]
> Bu belge, [İngilizce README](README.md) dosyasının çevirisidir. İkisi arasında fark olursa İngilizce sürüm esas alınır. Geliştirme, depo yapısı ve adım adım dağıtım ayrıntıları yalnızca İngilizce olarak güncel tutulur.

> [!IMPORTANT]
> IAPStack bir ön sürüm yazılımıdır. Mevcut `v0.1.0-rc1` ve `v0.1.0-rc2`
> sürümleri, güncel sürüm kapısından ve güvenlik düzeltmelerinden önceye ait eski
> önizlemelerdir; üretim kullanımı için desteklenmezler. Varsayılan dal geliştirme
> aşamasındadır ve üç sağlayıcı için gerçek mağaza yaşam döngüsü kapısı henüz tamamlanmamıştır.

IAPStack, uygulama içi satın almaları doğrulamak ve mağaza işlemlerini kalıcı uygulama
erişim haklarına dönüştürmek için kendi sunucunuzda barındırılan bir backend ve kontrol
panelidir. Ekiplere mobil mağazalar genelinde backend'in sahip olduğu tek bir model sunar;
kimlik bilgileri, satın alma verileri ve erişim kararları ise ekiplerin kontrol ettiği
altyapıda kalır.

<p align="center">
  <code>Store&nbsp;&nbsp;→&nbsp;&nbsp;Verify&nbsp;&nbsp;→&nbsp;&nbsp;Access&nbsp;&nbsp;→&nbsp;&nbsp;Deliver</code>
</p>

| Bir kez doğrulayın, her yerde | Makbuzlar değil, erişim hakları | Çalıştırması sizde |
| --- | --- | --- |
| Apple App Store, Google Play ve Huawei AppGallery için mağazadan bağımsız tek bir modelin arkasında sunucu tarafı doğrulama. | Uygulamalar ham makbuzları ve mağaza durumlarını yorumlamak yerine müşterinin neye erişebileceğini sorar. | Varsayılan olarak kendi sunucunuzda: altyapı, kimlik bilgileri, satın alma verileri ve erişim kararları sizindir. |

<a id="current-scope"></a>

## Mevcut kapsam

- Apple App Store, Google Play ve Huawei AppGallery'deki abonelikler ve tüketilemeyen
  ömür boyu satın almalar için sunucu tarafı doğrulama
- PostgreSQL tabanlı, mağazadan bağımsız bir işlem ve erişim hakkı modeli
- Kalıcı bildirim alımı, yaşam döngüsü mutabakatı ve imzalı giden webhook'lar
- Sürümlü HTTP/OpenAPI sözleşmeleriyle birlikte kimlik doğrulamalı bir operasyon paneli
  (Sistem / Açık / Koyu temalar, daraltılabilir bir proje kenar çubuğu ve incelenebilir
  etkinlik ve abonelik grafikleri)
- Mevcut üç sağlayıcının tamamı için sağlayıcıdan bağımsız ve mağazaya özgü Flutter paketleri
- Docker ve belgelenmiş bulut hedefleriyle kompakt ya da ayrık API/worker dağıtımı

Tüketilebilir ürün teslimi, müşteri taşıma ve takma ad birleştirme, barındırılan işletim
ve yukarıdaki üç mağaza dışındaki mağazalar v0.1 kapsamı dışındadır. Sınırın tamamı için
[v0.1 kapsamına](docs/v0.1-scope.md) bakın.

<a id="sdk-roadmap"></a>

## SDK yol haritası

Flutter, v0.1 sürüm kapsamındaki tek mobil istemci olmaya devam ediyor: sağlayıcıdan
bağımsız bir HTTP istemcisi ile Apple, Google Play ve Huawei yardımcı paketleri. Ek SDK
kodu, [#85](https://github.com/imariman/iapstack/issues/85) numaralı genişletme planına
uygun olarak zaten depoda bulunuyor. Bu kodun varlığı v0.1 sürüm kapısını genişletmez
ve bu paketleri üretim sürümü hâline getirmez.

| SDK | Depoda uygulananlar | Kalan işler |
| --- | --- | --- |
| [Go güvenilir sunucu](sdk/go/README.md) | Müşteri oturumları, sunucu tarafı erişim hakkı sorgulama ve imzalı webhook işleme | İlk sürümlü modül yayını ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [TypeScript güvenilir sunucu](sdk/typescript/README.md) | Müşteri oturumları, erişim hakkı sorgulama, imzalı webhook'lar ve kurulabilir ESM/tip arşivleri | İlk npm yayını ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [iOS (Swift)](sdk/ios/README.md) | HTTP istemcisi, StoreKit 2 yardımcı paketi, StoreKit yapılandırmalı çalıştırılabilir örnek, kök SwiftPM manifesti | İlk SDK sürüm etiketi ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [Android (Kotlin)](sdk/android/README.md) | HTTP istemcisi, Play Billing/HMS adaptörleri, güvenilir sunucu örneği ve hazırlanmış Maven ikili/kaynak jar'ları | İlk GitHub Maven yayını ([#141](https://github.com/imariman/iapstack/issues/141)) |
| [React Native](sdk/react-native/README.md) | HTTP istemcisi, yerel StoreKit/Play/Huawei köprüsü, yerel örnek uygulama ve paketlenmiş yardımcı paket kaynakları | İlk npm yayını ([#141](https://github.com/imariman/iapstack/issues/141)) |

[SDK sürüm kılavuzu](docs/sdk-releases.md); kurulumu, bağımsız ön sürüm numaralarını,
paket kontrollerini ve kayıt deposu kurulumunu açıklar. Bu depodaki kod, bir kayıt
deposunda yayımlanmış bir sürüm olduğu anlamına gelmez.

Kalıcı uygulama bearer token'ı güvenilir bir sunucu backend'inde kalır ve hiçbir zaman
bir mobil ikili dosyaya veya tarayıcı paketine konmamalıdır. Mobil istemciler kısa ömürlü
müşteri oturumları kullanır.

<a id="quick-start"></a>

## Hızlı başlangıç

Compose destekli Docker'a ve OpenSSL'e ihtiyacınız var. Bu deponun bir klonunda, yalnızca
yerel kullanım için gizli anahtarlar oluşturun ve yığının tamamını başlatın:

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

Oluşturulan değerleri güvenli bir yerde saklayın ve bu Compose birimiyle yeniden kullanın.
Değerleri yeniden oluşturmak, mevcut veritabanı parolasını veya koruma anahtarlarını
döndürmez.

Ardından `http://127.0.0.1:8080/dashboard/` adresini açın ve önyükleme (bootstrap)
yönetici anahtarıyla bağlanın. Kalıcı bir yönetici anahtarı oluşturup saklayın,
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` değişkenini ortamdan kaldırın ve API container'ını yeniden
oluşturun. [Operasyon kılavuzu](docs/operations.md); önyüklemeyi, anahtar döndürmeyi,
kapatmayı, yedeklemeleri, yükseltmeleri ve üretim güvenliği temel çizgisini açıklar.

<a id="deploy"></a>

## Dağıtım

Sağlayıcı kaynaklarını oluşturmadan önce hesap gereksinimlerini, ücretsiz katman
sınırlamalarını ve yığının tamamının maliyetlerini
[dağıtım hedefleri tablosunda](deploy/README.md) karşılaştırın.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

| Hedef | Yapı | Kılavuz |
| --- | --- | --- |
| Render | API'yi ve worker'ı çalıştıran tek bir ücretsiz web servisi, test için ücretsiz bir PostgreSQL veritabanı ve ücretli kompakt ya da ayrık Blueprint'ler | [Render kılavuzu](deploy/render/README.md) |
| Heroku | Sürekli çalışan iki Basic dyno üzerinde API, worker ve yönetilen PostgreSQL; ücretsiz çalışma ortamı yok | [Heroku kılavuzu](deploy/heroku/README.md) |
| DigitalOcean | Worker ve migration işiyle App Platform ya da daha düşük maliyetli bir Droplet | [App Platform kılavuzu](deploy/digitalocean/README.md) · [Droplet kılavuzu](deploy/digitalocean/droplet.md) |
| Fly.io | Özel Managed Postgres ile süreç grupları; yalnızca kısa bir ücretsiz deneme | [Fly.io kılavuzu](deploy/fly/README.md) |
| Koyeb | Resmî CLI üzerinden yönetilen veritabanı, HTTPS API ve özel worker | [Koyeb kılavuzu](deploy/koyeb/README.md) |
| Railway | PostgreSQL ve API ile worker'ı çalıştıran tek bir servis; Hobby planında ayda yaklaşık USD 5 | [Railway kılavuzu](deploy/railway/README.md) |
| Coolify | Deponun kendi Docker Compose tanımından, kendi sunucunuzda yığının tamamı | [Coolify kılavuzu](deploy/coolify/README.md) |
| Tek VM | Sürekli çalışan en ucuz seçenek: bir Cloudflare Tunnel arkasında, tek bir Linux makinesinde PostgreSQL, migration'lar, API ve worker | [Tek VM kılavuzu](deploy/vps/README.md) |

### En düşük maliyetli seçenek: tek bir VM

Tek bir Linux makinesi her şeyi
[`deploy/vps/compose.yaml`](deploy/vps/compose.yaml) dosyasından çalıştırır: özel bir Docker
ağında PostgreSQL, tek seferlik bir migration container'ı, API'yi ve kalıcı worker'ı içeren
tek bir kompakt süreç ve bir Cloudflare Tunnel bağlayıcısı; böylece VM'de gelen bağlantılara
açık port gerekmez. Worker sürekli çalışan bir sürece ihtiyaç duyar; bu yüzden küçük ve
sürekli açık bir VM, sıfıra ölçeklenen platformlardan daha uygundur.

| Sunucu | Tipik maliyet | Uygunluk |
| --- | --- | --- |
| Oracle Cloud Always Free, Ampere A1 | USD 0 | 4 Arm OCPU'ya ve 24 GB RAM'e kadar; tek tıklamalı Resource Manager yığını. |
| Google Cloud e2-micro (Free Tier, ABD bölgeleri) | USD 0–4/ay | Düşük trafik için 1 GB RAM; rehberli Cloud Shell anlatımı. |
| Hetzner Cloud CAX11 / CX22 | Yaklaşık EUR 4–5/ay | Öngörülebilir faturalandırma; tek bir cloud-init dosyasıyla kurulur. |
| DigitalOcean Droplet `s-1vcpu-1gb` / `s-1vcpu-2gb` | Ayda USD 6 / 12 | Genel IPv4 dahil; Hetzner ile aynı cloud-init dosyası. |
| Zaten işlettiğiniz bir sunucu | USD 0 | NAT arkasında çalışır, ancak mağaza bildirimleri kesintisiz çalışma süresi gerektirir. |

Ayrıca DNS'i Cloudflare'in ücretsiz planında bulunan bir alan adına ihtiyacınız var. Kaynakları
oluşturmadan önce her sağlayıcının güncel koşullarını doğrulayın. Kurulum adımları, yedeklemeler
ve yükseltmeler [İngilizce README'nin Deploy bölümünde](README.md#deploy) ve
[tek VM kılavuzunda](deploy/vps/README.md) açıklanmıştır.

<a id="design-principles"></a>

## Tasarım ilkeleri

**Varsayılan olarak kendi sunucunuzda.** Altyapı, kimlik bilgileri, satın alma verileri ve erişim kararları sizindir.

**Mağazalar adaptördür.** Mağazaya özgü API'ler ortak sözleşmelerin arkasında yalıtılmış kalır; çekirdek alan tek bir pazar yerine bağımlı değildir.

**Ürün sınırı erişim haklarıdır.** Uygulamalar ham makbuzları veya mağaza durumlarını yorumlamak yerine müşterinin neye erişebileceğini sorar.

**Her yere dağıtın.** Hedef, belirli bir bulut platformuna bağımlılık yerine basit bir container dağıtımıdır.

**Bağımsız uygulama.** Davranış ve güvenlik gereksinimlerini resmî mağaza belgeleri tanımlar. Birlikte çalışabilirlik ve uç durumlar için başka açık kaynaklı projeler incelenebilir; ancak IAPStack'in kodu bağımsız olarak yazılır.

<a id="documentation"></a>

## Dokümantasyon

Dokümantasyon İngilizce yazılmıştır.

- [v0.1 kapsamı ve mimarisi](docs/v0.1-scope.md)
- [Mimari Karar Kayıtları](docs/adr/README.md)
- [HTTP API v1 ve sağlayıcı bildirim sözleşmeleri](docs/api-v1.md)
- [Makine tarafından okunabilir OpenAPI v1 sözleşmesi](contracts/openapi/v1.yaml)
- [Operasyon ve panel kılavuzu](docs/operations.md)
- [v0.1 tehdit modeli ve güvenlik sürüm kontrol listesi](docs/security.md)
- [Güvenlik politikası ve güvenlik açığı bildirimi](SECURITY.md)
- [Katkı kılavuzu](CONTRIBUTING.md)
- [Destek kılavuzu](SUPPORT.md)
- [Sağlayıcıdan bağımsız Flutter SDK'sı](sdk/flutter/iapstack/README.md)
- [Yerel iOS Swift SDK'sı ve StoreKit yardımcı paketi](sdk/ios/README.md)
- [Go güvenilir sunucu SDK'sı](sdk/go/README.md)
- [TypeScript güvenilir sunucu SDK'sı](sdk/typescript/README.md)

<a id="development"></a>

## Geliştirme

Kaynaktan derleme, yapılandırma, testler ve depo yapısı
[İngilizce README'nin Development bölümünde](README.md#development) açıklanmıştır. Bir pull
request açmadan önce [katkı kılavuzunu](CONTRIBUTING.md) okuyun.

<a id="roadmap"></a>

## Yol haritası

- [x] Çekirdek alanı, genel API sözleşmelerini ve kalıcılık modelini tanımlamak
- [x] İlk Apple App Store, Google Play ve Huawei AppGallery adaptörlerini uygulamak
- [x] PostgreSQL migration'larını ve üretim düzeninde bir Docker kurulumunu eklemek
- [x] Sağlayıcıdan bağımsız ve sağlayıcıya özgü üç Flutter SDK'sını ve paneli geliştirmek
- [x] Yaşam döngüsü mutabakatını ve imzalı giden webhook'ları desteklemek
- [x] İstemci uyumluluk kontrolleriyle makine tarafından okunabilir bir v1 API sözleşmesi yayımlamak
- [x] Go, TypeScript, Swift, Kotlin ve React Native SDK'larını uygulamak
- [x] Müşteri taşıma, takma ad birleştirme, RevenueCat içe aktarma ve finansal rapor içe aktarma desteği eklemek
- [x] Kubernetes dağıtım manifestlerini eklemek
- [ ] Apple App Store, Google Play ve Huawei yaşam döngüsü sürüm kapılarını tamamlamak ve `v0.1.0-rc.1` sürümünü yayımlamak
- [ ] Aday sürüm geri bildirimlerini çözmek ve kararlı `v0.1.0` sürümünü yayımlamak
- [ ] Sürümlü sunucu, Android, iOS ve React Native paketlerini yayımlamak ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Amazon Appstore ve ek mağaza adaptörleri eklemek

Tamamlanan maddeler depodaki uygulamaları ifade eder; üretim sürümüne hazır olunduğunu göstermez.

<a id="license"></a>

## Lisans

[Apache License 2.0](LICENSE) ile lisanslanmıştır.

Bu projeye katılarak [Davranış Kuralları](CODE_OF_CONDUCT.md)'na uymayı kabul edersiniz.
Katkı ve destek yolları için [CONTRIBUTING.md](CONTRIBUTING.md) ve [SUPPORT.md](SUPPORT.md)
dosyalarına bakın.
