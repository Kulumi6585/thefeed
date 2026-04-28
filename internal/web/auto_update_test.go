package web

import (
	"reflect"
	"testing"
)

func TestNormaliseAutoUpdateList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"empty", []string{}, []string{}},
		{"strip @", []string{"@one", "two"}, []string{"one", "two"}},
		{"trim whitespace", []string{"  one  ", "\ttwo\n"}, []string{"one", "two"}},
		{"drop empties", []string{"one", "", "  ", "@", "two"}, []string{"one", "two"}},
		{"dedupe preserves order", []string{"a", "b", "@a", "c", "b"}, []string{"a", "b", "c"}},
		{"dedupe across @ form", []string{"@chan", "chan"}, []string{"chan"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normaliseAutoUpdateList(c.in)
			if got == nil {
				got = []string{}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("normaliseAutoUpdateList(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
