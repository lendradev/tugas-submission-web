package pathsafe

import "testing"

func TestClean(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isErr bool
	}{
		{in: "soal.pdf", want: "soal.pdf"},
		{in: "uts/bagian-a.md", want: "uts/bagian-a.md"},
		{in: "./soal.pdf", want: "soal.pdf"},
		{in: "a//b//c.txt", want: "a/b/c.txt"},
		{in: "/abs/path", isErr: true},
		{in: "../escape", isErr: true},
		{in: "a/../../escape", isErr: true},
		{in: "", isErr: true},
		{in: ".git/config", isErr: true},
		{in: "a/.git/config", isErr: true},
		{in: "na\x00me", isErr: true},
		{in: "na\nme", isErr: true},
	}
	for _, c := range cases {
		got, err := Clean(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("Clean(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Clean(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJoin(t *testing.T) {
	cases := []struct {
		dir   string
		name  string
		want  string
		isErr bool
	}{
		{dir: "", name: "soal.pdf", want: "soal.pdf"},
		{dir: "uts", name: "a.pdf", want: "uts/a.pdf"},
		{dir: "uts", name: "../a.pdf", isErr: true},
		{dir: "uts", name: "sub/a.pdf", isErr: true},
		{dir: "uts", name: ".hidden", isErr: true},
		{dir: "uts", name: "", isErr: true},
	}
	for _, c := range cases {
		got, err := Join(c.dir, c.name)
		if c.isErr {
			if err == nil {
				t.Errorf("Join(%q,%q) = %q, want error", c.dir, c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Join(%q,%q) error: %v", c.dir, c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("Join(%q,%q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
}

func TestDirBase(t *testing.T) {
	if got := Dir("a/b/c.txt"); got != "a/b" {
		t.Errorf("Dir = %q, want a/b", got)
	}
	if got := Dir("c.txt"); got != "" {
		t.Errorf("Dir = %q, want kosong", got)
	}
	if got := Base("a/b/c.txt"); got != "c.txt" {
		t.Errorf("Base = %q, want c.txt", got)
	}
}
