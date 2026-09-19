import type { Dict } from "@/locales/en";
import { agentArea } from "@/locales/tr/agentArea";
import { boardArea } from "@/locales/tr/boardArea";
import { chatArea } from "@/locales/tr/chatArea";
import { content } from "@/locales/tr/content";
import { frame } from "@/locales/tr/frame";
import { lib } from "@/locales/tr/lib";
import { operations } from "@/locales/tr/operations";
import { projectAdmin } from "@/locales/tr/projectAdmin";
import { settingsPages } from "@/locales/tr/settingsPages";
import { setup } from "@/locales/tr/setup";

// Turkish dictionary. Typed as Dict — must mirror en.ts keys exactly.
export const tr: Dict = {
  common: {
    save: "Kaydet",
    saving: "Kaydediliyor...",
    cancel: "İptal",
    edit: "Düzenle",
    refresh: "Yenile",
    resetDefault: "Varsayılana dön",
    actionFailed: "İşlem başarısız",
    saved: "Kaydedildi",
    saveFailed: "Kaydetme başarısız",
    errorBoundary: {
      title: "Bir şeyler ters gitti",
      body: "Beklenmeyen bir hata oluştu ve ekran çizilemedi. Sayfayı yeniden yükleyip tekrar deneyebilirsiniz.",
      retry: "Tekrar dene",
      reload: "Sayfayı yenile",
    },
    configError: {
      title: "Yapılandırma hatası",
      body: "Bu sürümde yerel sunucu için API anahtarı bulunmuyor; bu nedenle her istek reddedilir. VITE_API_KEY değerini (sunucudaki SERVER_API_KEY ile aynı olmalıdır) tanımlayıp uygulamayı yeniden derleyin ya da sunucuda web oturumunu etkinleştirin.",
      missing: "Eksik:",
    },
  },
  auth: {
    login: {
      title: "Oturum aç",
      subtitle: "TaskTrooper'a devam etmek için kullanıcı adınızı ve parolanızı girin.",
      username: "Kullanıcı adı",
      password: "Parola",
      submit: "Oturum aç",
      submitting: "Oturum açılıyor…",
      invalid: "Kullanıcı adı veya parola hatalı.",
      locked: "Çok sayıda hatalı deneme yapıldı. {minutes} dakika sonra yeniden deneyin.",
      failed: "Oturum açılamadı. Lütfen yeniden deneyin.",
      expired: "Oturumunuzun süresi doldu. Lütfen yeniden oturum açın.",
    },
    session: {
      signedInAs: "{name} olarak oturum açıldı",
      signOut: "Oturumu kapat",
    },
  },
  settings: {
    language: {
      label: "Dil",
      help: "Asistan yanıtları ve sistem talimatları için kullanılır.",
    },
    loadFailed: "Ayarlar yüklenemedi",
    savedToast: "Ayarlar kaydedildi",
    agentConcurrency: {
      title: "Eşzamanlı ajan sayısı",
      description: "Bu makinede, tüm ajan çalışma ortamları genelinde aynı anda kaç ajan oturumunun çalışabileceğini belirler. Sınırı artırdığınızda kuyruktaki işler hemen başlar; azalttığınızda çalışan oturumlar durdurulmaz, yeni oturumlar sayı sınırın altına inene kadar bekler.",
      label: "Aynı anda çalışacak ajan",
      occupancy: "{active} / {limit} çalışıyor, {waiting} kuyrukta",
      occupancyUnlimited: "{active} çalışıyor, sınır yok",
      savedToast: "Eşzamanlı ajan sınırı kaydedildi",
    },
    github: {
      statusUnavailable: "Durum alınamadı.",
      connected: "✓ Bağlı: {login} — ajanlar private repo oluşturabilir, push edebilir ve taslak PR açabilir.",
      disconnect: "Bağlantıyı kes",
      connect: "Token'ı kaydet",
      tokenPlaceholder: "ghp_… veya github_pat_…",
      tokenHelp:
        "GitHub → Settings → Developer settings'ten alınan bir personal access token. repo, admin:repo_hook ve read:org izinleri gerekir. Saklanmadan önce GitHub'a karşı doğrulanır ve bu makinede şifreli tutulur.",
      connectedToast: "GitHub bağlandı",
      connectFailedToast: "GitHub bağlantısı başarısız",
      disconnectedToast: "GitHub bağlantısı kaldırıldı",
    },
    vercel: {
      statusUnavailable: "Durum alınamadı.",
      connected: "✓ Bağlı: {login}",
      disconnect: "Bağlantıyı kes",
      connect: "Bağlan",
      tokenLabel: "Erişim token'ı",
      tokenPlaceholder: "Vercel erişim token'ını yapıştır",
      tokenHelp:
        "vercel.com/account/tokens adresinden, projelerinin bulunduğu takıma kapsamlı bir token oluştur. Token bir kez doğrulanır, sunucuda şifreli saklanır ve bir daha gösterilmez.",
      team: "Kapsam",
      personalAccount: "Kişisel hesap",
      teamSaved: "Vercel kapsamı kaydedildi",
      connectedToast: "Vercel bağlandı",
      connectFailedToast: "Vercel bağlantısı başarısız",
      disconnectedToast: "Vercel bağlantısı kaldırıldı",
      apps: {
        title: "Uygulama bağlantıları",
        subtitle:
          "Bir uygulama seç. TaskTrooper repo ağacını ve Vercel projelerini okuyup frontend ve backend'in nereye deploy olduğunu bulur; anlayamazsa sen seçersin.",
        selectApp: "Uygulama seç",
        noApps: "Henüz uygulama yok — önce bir repo ekle.",
        detecting: "Repo ve Vercel projeleri inceleniyor…",
        detectFailed: "Tespit başarısız",
        noAreas: "Bu uygulamanın barındırılacak frontend veya backend alanı yok — yalnızca backend ve frontend projeleri bağlanır.",
        warnings: "Notlar",
        area: { root: "Tüm repo", frontend: "Frontend", backend: "Backend", mobile: "Mobil", worker: "Worker" },
        directory: "Klasör",
        linked: "{name} projesine bağlı",
        linkedProvider: "{provider} olarak kaydedildi",
        linkedBy: { detected: "otomatik tespit", user: "senin seçimin" },
        openProject: "Aç",
        unlink: "Bağlantıyı kaldır",
        unlinked: "Bağlantı kaldırıldı",
        hints: "Ağacın söyledikleri",
        detected: "Tespit edildi: {name}",
        detectedHelp: "Kanıt: {reason}.",
        confirm: "Bağla",
        ambiguous: "Birden fazla Vercel projesi bu olabilir — doğrusunu seç.",
        none: "Nereye deploy olduğu anlaşılamadı. Vercel projesini seç ya da nerede yaşadığını kaydet.",
        pickProject: "Vercel projesi",
        pickProjectPlaceholder: "Proje seç",
        loadingProjects: "Projeler yükleniyor…",
        noProjects: "Bu kapsamda proje yok.",
        elsewhere: "Başka yerde barınıyor",
        elsewherePlaceholder: "Sağlayıcı seç",
        record: "Kaydet",
        recorded: "Kaydedildi — bu alan bir daha sorulmayacak.",
        linkSaved: "{name} projesine bağlandı",
        notConnected: "Projeleri görmek için yukarıdan Vercel'i bağla.",
        reasons: {
          project_json: "çalışma kopyasındaki .vercel/project.json",
          git_link_dir: "bu repoya git ile bağlı ve bu klasörden build alıyor",
          git_link: "bu repoya git ile bağlı",
          name: "eşleşen proje adı",
        },
      },
    },
    boilerplate: {
      title: "Boilerplate Kataloğu",
      descPrefix: "Agent'lar sıfırdan kod yazmadan önce bu repodaki",
      descMid: "dosyasını arar; eşleşen bir boilerplate varsa onu kopyalayarak başlar.",
      descSuffix: "veya tam URL kabul edilir.",
      loadFailed: "Ayarlar yüklenemedi",
    },
    notifications: {
      title: "Masaüstü bildirimleri",
      help: "Dikkatini gerektiren pano olayları için yerel bildirimler. Pencereyi kapatsan bile çalışmaya devam eder.",
      unavailable: "Masaüstü bildirimleri yalnızca TaskTrooper uygulamasında kullanılabilir, tarayıcıda değil.",
      enabled: "Etkin",
      analizReview: "İncelemen için hazır analiz",
      humanUat: "UAT'ını bekliyor",
      humanNeeded: "Bir agent insan kararına ihtiyaç duyuyor",
      agentComments: "Yeni agent yorumları",
      loadFailed: "Ayarlar yüklenemedi",
      saveFailed: "Kaydetme başarısız",
    },
  },
  agentArea,
  boardArea,
  chatArea,
  content,
  frame,
  lib,
  operations,
  projectAdmin,
  settingsPages,
  setup,
};
