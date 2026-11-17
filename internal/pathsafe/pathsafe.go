// Package pathsafe menyediakan normalisasi path berkas agar aman dipakai
// sebagai path di dalam repository GitHub.
package pathsafe

import (
	"errors"
	"path"
	"strings"
)

// ErrInvalidPath dikembalikan untuk path yang tidak aman.
var ErrInvalidPath = errors.New("path tidak valid")

// ErrOutsideRoot dikembalikan saat path keluar dari folder user.
var ErrOutsideRoot = errors.New("path berada di luar folder user")

// MaxPathLength batas panjang path relatif.
const MaxPathLength = 240

// Clean menormalisasi path relatif: menolak path absolut, "..", karakter kontrol,
// dan segment kosong. Path hasil selalu relatif dan tanpa "./".
func Clean(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "", ErrInvalidPath
	}
	if len(p) > MaxPathLength {
		return "", ErrInvalidPath
	}
	if strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidPath
		}
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, seg := range parts {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", ErrOutsideRoot
		}
		if seg == ".git" {
			return "", ErrInvalidPath
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return "", ErrInvalidPath
	}
	cleaned := path.Join(out...)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", ErrOutsideRoot
	}
	return cleaned, nil
}

// Join menggabungkan folder dan nama berkas lalu membersihkannya.
func Join(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrInvalidPath
	}
	if strings.ContainsAny(name, "/\\") {
		return "", ErrInvalidPath
	}
	if name == "." || name == ".." {
		return "", ErrInvalidPath
	}
	if strings.HasPrefix(name, ".") {
		return "", ErrInvalidPath
	}
	return Clean(path.Join(dir, name))
}

// Dir mengembalikan folder induk dari path relatif.
func Dir(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

// Base mengembalikan nama berkas dari path relatif.
func Base(p string) string { return path.Base(p) }
