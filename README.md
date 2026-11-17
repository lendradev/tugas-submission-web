# Tugas Submission

Web app **invite-only** untuk mengumpulkan tugas ke satu repository GitHub pada
path `tugas/{username}/`. Mahasiswa tidak perlu memasang Git: cukup login
di browser, seret berkas, lalu sistem menyimpannya ke repository secara
tertunda (debounce adaptif).

Antarmuka mirip Google Drive: sidebar folder, breadcrumb, tampilan daftar/kisi,
pencarian, seret-atau-lepas untuk unggah, dan dashboard admin untuk mengelola
akun, kuota, serta jadwal sinkronisasi.

## Fitur

- **Unggah tanpa Git CLI** — berkas masuk ke `tugas/{username}/` di repository.
- **Sinkronisasi tertunda adaptif** — jeda dasar 5 menit, minimal 1 menit,
  maksimal 30 menit. Perubahan berulang memperpanjang jeda secara
  otomatis (`base * 2^(burst-2)`), sehingga unggahan beruntun digabung jadi
  satu commit.
- **Mirror hemat storage** — salinan lokal dihapus begitu commit berhasil
  didorong ke repository. Metadata (nama, ukuran, SHA blob) tetap di SQLite.
- **Anti-DoS berlapis** — token bucket per IP/per pengguna/per endpoint, batas
  ukuran berkas, kuota total, batas jumlah berkas per permintaan, semaphore
  unggah, batas bad request, timeout server.
- **Isolasi antar pengguna** — path di luar folder pengguna ditolak, termasuk
  `..`, path absolut, karakter kontrol, dan nama berawalan titik.
- **Panel admin** — CRUD akun, ubah peran, nonaktifkan, reset password, paksa
  push, ubah batas ukuran/kuota/jeda sinkronisasi.
- **Mode gelap** — mengikuti `prefers-color-scheme` sistem.
- **Aksesibilitas** — navigasi keyboard, label ARIA pada tombol ikon, kontras
  minimal AA.

## Tumpukan teknologi

| Komponen | Pilihan | Alasan |
|---|---|---|
| Bahasa | Go 1.26+ (`net/http` tanpa framework) | binary tunggal, mudah 배포 di VPS kecil |
| Basis data | SQLite via `modernc.org/sqlite` (WAL) | pure Go, tanpa CGO, cocok untuk ARM64 |
| Frontend | Alpine.js 3 (build CSP) | tanpa build step, tanpa CDN eksternal |
| Hashing | `golang.org/x/crypto/bcrypt` (cost 12) | standar untuk password |
| Waktu | `golang.org/x/time/rate` | token bucket rate limit |

Tanpa CGO → binary statis, bisa berjalan di `linux/arm64` maupun `linux/amd64`.

## Menjalankan

### Dengan Docker (disarankan)

```bash
docker build -t tugas-submission .
docker run -d --name tugas-submission \
  -p 8080:8080 \
  -v "$PWD/data:/data" \
  --env-file .env \
  tugas-submission
```

### Tanpa Docker

```bash
go mod download
CGO_ENABLED=0 go build -o bin/tugas-submission .
./bin/tugas-submission
```

Aplikasi membaca konfigurasi dari environment (lihat `.env.example`).
Tanpa `TS_GITHUB_TOKEN` yang valid, unggahan tetap tersimpan di server tetapi
belum bisa didorong ke repository.

## Konfigurasi

| Variabel | Default | Keterangan |
|---|---|---|
| `TS_ADDR` | `127.0.0.1:8080` | alamat listen |
| `TS_DATA_DIR` | `./data` | folder database + staging |
| `TS_REPO_OWNER` | – | pemilik repository target |
| `TS_REPO_NAME` | – | nama repository target |
| `TS_REPO_BRANCH` | `main` | branch tujuan |
| `TS_GITHUB_TOKEN` | – | fine-grained PAT dengan akses tulis repo |
| `TS_GITHUB_API` | `https://api.github.com` | endpoint API (untuk Gitea/self-hosted) |
| `TS_GITEA_MODE` | `false` | pakai Contents API (Gitea) alih-alih Git Data API |
| `TS_ADMIN_USERNAME` | `admin` | akun admin awal |
| `TS_ADMIN_PASSWORD` | – | password admin awal (wajib saat pertama jalan) |
| `TS_SESSION_TTL_HOURS` | `168` | masa berlaku sesi |
| `TS_TRUSTED_PROXY` | `false` | aktifkan bila di belakang reverse proxy |
| `TS_TLS_CERT` / `TS_TLS_KEY` | – | aktifkan TLS langsung (biasanya lewat reverse proxy) |

Token hanya dibaca dari environment dan tidak pernah ditulis ke log atau
respons API.

### Membuat token GitHub

1. GitHub → Settings → Developer settings → Fine-grained tokens.
2. Repository access: pilih repository tujuan saja.
3. Permissions → Repository permissions → **Contents: Read and write**.
4. Salin token ke `TS_GITHUB_TOKEN`.

## Penggunaan

1. Admin login, buka `/admin`, buat akun untuk setiap mahasiswa.
2. Mahasiswa login, seret berkas ke area unggah (bisa banyak berkas sekaligus).
3. Berkas masuk antrean; status di sidebar menunjukkan jumlah berkas yang
   menunggu push dan kapan push berikutnya.
4. Setelah jeda debounce berakhir, scheduler mendorong semua antrean milik satu
   pengguna dalam satu commit, lalu menghapus salinan lokalnya.
5. Admin bisa menekan **Push** untuk memaksa sinkronisasi saat itu juga.

## API

Semua endpoint di bawah memakai session cookie. Endpoint yang mengubah data
memerlukan header `X-CSRF-Token` sesuai nilai dari session.

| Method | Path | Keterangan |
|---|---|---|
| `POST` | `/login` | login, mengembalikan session cookie + token CSRF |
| `POST` | `/logout` | keluar |
| `GET` | `/api/list?path=` | daftar isi folder pengguna |
| `GET` | `/api/status` | status sinkronisasi ringkas |
| `GET` | `/api/settings` | status sinkronisasi dan kuota pengguna |
| `POST` | `/api/upload?path=` | unggah berkas (multipart, field `files`) |
| `POST` | `/api/rename` | ubah nama berkas (`from`, `to`) |
| `POST` | `/api/delete` | hapus berkas (`paths`) |
| `GET` | `/download?path=` | unduh berkas dari repository |
| `GET` | `/admin/users` | daftar pengguna + statistik |
| `POST` | `/admin/users` | buat akun (`username`, `display_name`, `password`, `role`) |
| `POST` | `/admin/users/{id}/role` | ubah peran |
| `POST` | `/admin/users/{id}/active` | aktif/nonaktifkan |
| `POST` | `/admin/users/{id}/password` | reset password |
| `POST` | `/admin/users/{id}/push` | paksa sinkronisasi |
| `POST` | `/admin/users/{id}/delete` | hapus akun |
| `GET`/`POST` | `/admin/settings` | baca/simpan pengaturan |

## Pengembangan

```bash
go vet ./...
go test ./...
```

### Menyegarkan Alpine.js

Berkas `internal/web/static/alpine.min.js` divendor dengan versi terkunci dan
dicek hash-nya:

```bash
./scripts/vendor.sh
```

### Menjalankan integration test

Butuh container Gitea lokal sebagai pengganti GitHub:

```bash
./scripts/setup-gitea.sh
./scripts/run-integration.sh
```

### Mengambil tangkapan layar UI

```bash
TS_BASE_URL=http://172.17.0.4:8080 \
TS_BROWSER_CONTAINER=<container-puppeteer> \
./scripts/capture-screenshots.sh
```

Hasil disimpan di folder `screenshots/` pada root project.

## Batasan yang diketahui

- Sinkronisasi hanya satu arah (server → repository). Perubahan di repository
  secara manual akan ditimpa pada push berikutnya.
- Hapus berkas baru berlaku pada sinkronisasi berikutnya, bukan seketika.
- Branch/PR/pull request belum didukung.
- Struktur folder di repository mengikuti path unggahan apa adanya; tidak ada
  flatten otomatis.

## Lisensi

MIT — lihat [LICENSE](LICENSE).
