package server

import (
	"reflect"
	"testing"
)

func TestParseArraySpec(t *testing.T) {
	cases := []struct {
		in   string
		want []int
		err  bool
	}{
		{"1-3", []int{1, 2, 3}, false},
		{"5", []int{5}, false},
		{"1,3,5", []int{1, 3, 5}, false},
		{"2-4:2", []int{2, 4}, false},
		{"5-6,10", []int{5, 6, 10}, false},
		{"3-1", []int{1, 2, 3}, false},
		{"abc", nil, true},
		{"x", nil, true},
		{"-", nil, true},
	}
	for _, c := range cases {
		got, err := parseArraySpec(c.in)
		if c.err {
			if err == nil {
				t.Fatalf("parseArraySpec(%q): expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseArraySpec(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("parseArraySpec(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
