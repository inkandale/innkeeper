package merge

import (
	"reflect"
	"strings"
	"testing"
)

func TestSentences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"two sentences", "Hail. The forge is cold.", []string{"Hail. ", "The forge is cold."}},
		{"markdown and quotes", "*She waves.* \"Hi!\" Then", []string{"*She waves.* ", "\"Hi!\" ", "Then"}},
		{"newline", "Line one\nLine two", []string{"Line one\n", "Line two"}},
		{"ellipsis", "Wait… what?", []string{"Wait… ", "what?"}},
		{"decimal", "v1.2 is out", []string{"v1.2 is out"}},
		{"empty", "", nil},
		{"crlf", "One\r\nTwo", []string{"One\r\n", "Two"}},
		{"multiple spaces", "Hi.   There", []string{"Hi.   ", "There"}},
		{"blank line", "A\n\nB", []string{"A\n\n", "B"}},
		{"curly closers", "He said “no.” Then left.)", []string{"He said “no.” ", "Then left.)"}},
		{"trailing space", "End. ", []string{"End. "}},
		{"invalid utf8", "a\xff. b", []string{"a\xff. ", "b"}},
		{"whitespace only", "   ", []string{"   "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sentences(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sentences(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if strings.Join(got, "") != tt.in {
				t.Errorf("tokens do not rejoin to %q", tt.in)
			}
		})
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"a bc  d", []string{"a ", "bc  ", "d"}},
		{"x\ny", []string{"x\n", "y"}},
		{"  lead", []string{"  lead"}},
		{"", nil},
	}
	for _, tt := range tests {
		got := words(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("words(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Join(got, "") != tt.in {
			t.Errorf("tokens do not rejoin to %q", tt.in)
		}
	}
}
