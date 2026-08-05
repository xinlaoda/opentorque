package queue

import "testing"

func TestParseList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a", []string{"a"}},
		{"a,b", []string{"a", "b"}},
		{" a , b ,a ", []string{"a", "b"}},
		{"x y z", []string{"x", "y", "z"}},
		{"", nil},
		{" , ", nil},
	}
	for _, c := range cases {
		got := ParseList(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("ParseList(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ParseList(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
