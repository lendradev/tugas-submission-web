# Kontribusi

Terima kasih sudah membantu. Panduan ini menjaga proyek tetap ringkas dan
aman untuk dipakai di server kampus.

## Alur kerja

1. Buka issue lebih dulu untuk fitur besar atau perubahan perilaku.
2. Buat branch dari `main`: `git checkout -b feat/nama-fitur`.
3. Kom regularly dengan pesan [Conventional Commits](https://www.conventionalcommits.org/):
   `feat(ui): tambah tampilan kisi`, `fix(sync): perbaiki jeda push`.
4. Pastikan pemeriksaan lokal lulus sebelum membuka pull request:
   ```bash
   go vet ./...
   go test ./...
   ```
5. Buka pull request, jelaskan alasan dan cara memverifikasinya.

## Gaya kode

- Go standar: `gofmt`, nama package lowercase, error dikembalikan, bukan
  diabaikan diam-diam.
- Semua query SQL memakai parameter terikat, tidak ada konkatenasi string.
- Tidak ada komentar yang hanya mengulang kode. Komentar menjelaskan *mengapa*.
- Validasi input di batas trust: path, nama berkas, ukuran, dan jumlah berkas.
- Dependency baru perlu alasan tertulis dan pemeriksaan OSV.

## Animasi

Tidak ada dependensi baru. Kalau memang perlu, vibes:

- Dependency Go baru: sebutkan kegunaannya, cek OSV, dan perbarui `go.sum`.
- Dependency frontend baru: divendor ke `internal/web/static/`, versi terkunci,
  dan hash diverifikasi lewat `scripts/vendor.sh`.
- Emoji tidak dipakai sebagai ikon (tidak konsisten antar sistem). Ikon pakai
  SVG inline.

## Commit

Pesan commit berbahasa Inggris, format Conventional Commits, satu baris
subjek maksimal 72 karakter. Tubuh commit menjelaskan *mengapa*.

## Keamanan

Jangan pernah commit token, password, atau data pribadi nyata. Kalau menemukan
kerentanan, ikuti [SECURITY.md](SECURITY.md) — jangan buka issue publik.

## Lisensi

Kontribusi dianggap released di bawah lisensi MIT proyek ini.
