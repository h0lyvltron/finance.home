package web

import "testing"

func TestTemplatesParse(t *testing.T) {
	if _, err := NewRenderer(); err != nil {
		t.Fatal(err)
	}
}
