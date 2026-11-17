# Keamanan

## Melaporkan kerentanan

Jangan membuka issue publik untuk dilaporkan kerentanan. Kirim laporan
privat ke maintainer melalui GitHub Security Advisories
(`Security` → `Report a vulnerability`) pada repository ini.

Sertakan:

- Versi aplikasi (tag commit) dan konfigurasi yang relevan (tanpa token).
- Langkah reproduksi minimal.
- Dampak yang diperkirakan.
- Usulan mitigasi bila ada.

Target BALASAN: 7 hari kerja. Kerentanan yang sudah dikonfirmasi akan
dirilis pada versi berikutnya, dan Credit akan diberikan bila Anda ingin.

## Cakupan yang di luar

- Pengujian agresif terhadap server yang tidak dikelola proyek ini.
- Laporan yang hanya berisi hasil pemindaian otomatis tanpa langkah
  reproduksi belum dianggap valid.

## Privasi data

Aplikasi dirancang untuk minimise data:

- Yang disimpan: username, nama tampilan, hash password, daftar berkas
  (nama, ukuran, waktu, SHA blob), dan log perubahan.
- Yang tidak disimpan: isi berkas setelah commit berhasil (mirror lokal
  dihapus), email, nomor telepon, dan data akademik lain.
- Token GitHub hanya dibaca dari environment, tidak pernah ditulis ke log,
  respons API, atau basis data.
- Seluruh query memakai parameter terikat; path berkas divalidasi terhadap
  traversal sebelum menyentuh sistem berkas.

## Hardening yang sudah diterapkan

- Password di-hash dengan bcrypt cost 12.
- Sesi memakai token acak dari `crypto/rand`, cookie `HttpOnly` + `SameSite=Lax`.
- Token CSRF per sesi, diverifikasi pada setiap request yang mengubah data.
- Rate limit token bucket berlapis (per IP, per pengguna, per endpoint).
- CSP ketat: `script-src 'self'`, `style-src 'self'`, `img-src 'self' data:`.
- Batas ukuran berkas, kuota total, dan batas jumlah berkas per permintaan.
- Normalisasi path menolak `..`, path absolut, karakter kontrol, `.git`, dan
  nama berawalan titik.
