package inflect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInflectTable 逐词比对 testdata/expected.tsv（由 0.17 依赖的 pluralize 库实际跑出），
// 每行三列：词<TAB>单数<TAB>复数。词表太短就失去意义，所以要求至少 80 词。
func TestInflectTable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "expected.tsv"))
	if err != nil {
		t.Fatalf("读取期望词表失败: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 80 {
		t.Fatalf("期望词表仅 %d 词，要求至少 80 词", len(lines))
	}
	for _, line := range lines {
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("期望词表行格式错误: %q", line)
		}
		word, wantSing, wantPlur := cols[0], cols[1], cols[2]
		if got := Singular(word); got != wantSing {
			t.Errorf("Singular(%q) = %q, want %q", word, got, wantSing)
		}
		if got := Plural(word); got != wantPlur {
			t.Errorf("Plural(%q) = %q, want %q", word, got, wantPlur)
		}
	}
}

// TestInflectCommonWords 显式验证常用词表（单数↔复数双向）。
func TestInflectCommonWords(t *testing.T) {
	pairs := [][2]string{
		{"articles", "article"},
		{"movies", "movie"},
		{"images", "image"},
		{"notes", "note"},
		{"roles", "role"},
		{"files", "file"},
		{"pages", "page"},
		{"houses", "house"},
		{"horses", "horse"},
		{"courses", "course"},
		{"services", "service"},
		{"devices", "device"},
		{"addresses", "address"},
		{"statuses", "status"},
		{"boxes", "box"},
		{"buses", "bus"},
		{"heroes", "hero"},
		{"potatoes", "potato"},
		{"categories", "category"},
		{"companies", "company"},
		{"shoes", "shoe"},
		{"people", "person"},
		{"children", "child"},
		{"men", "man"},
		{"women", "woman"},
		{"mice", "mouse"},
		{"news", "news"},
		{"series", "series"},
		{"analyses", "analysis"},
		{"indices", "index"},
	}
	for _, p := range pairs {
		plural, singular := p[0], p[1]
		if got := Singular(plural); got != singular {
			t.Errorf("Singular(%q) = %q, want %q", plural, got, singular)
		}
		if got := Plural(singular); got != plural {
			t.Errorf("Plural(%q) = %q, want %q", singular, got, plural)
		}
	}
}
