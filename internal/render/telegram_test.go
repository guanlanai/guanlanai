package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapeHTML(t *testing.T) {
	got := EscapeHTML(`<b>BTC & ETH</b>`)
	want := `&lt;b&gt;BTC &amp; ETH&lt;/b&gt;`
	if got != want {
		t.Fatalf("EscapeHTML() = %q, want %q", got, want)
	}
}

func TestSplitShort(t *testing.T) {
	chunks := Split("hello", 10)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Fatalf("Split() = %#v", chunks)
	}
}

func TestSplitLong(t *testing.T) {
	input := strings.Repeat("币", 25)
	chunks := Split(input, 10)
	if len(chunks) != 3 {
		t.Fatalf("len(Split()) = %d, want 3", len(chunks))
	}
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) > 10 {
			t.Fatalf("chunk 超过限制: %d", utf8.RuneCountInString(chunk))
		}
	}
	if strings.Join(chunks, "") != input {
		t.Fatal("Split() 丢失内容")
	}
}
