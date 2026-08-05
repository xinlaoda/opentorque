package main

import (
	"testing"

	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func TestParseAttrs(t *testing.T) {
	tests := []struct {
		name   string
		tokens []string
		want   []dis.SvrAttrl
	}{
		{
			name:   "comma list stays a single value",
			tokens: []string{"route_destinations", "=", "short_q,long_q"},
			want:   []dis.SvrAttrl{{Name: "route_destinations", Value: "short_q,long_q", Op: 1}},
		},
		{
			name:   "resource sub-attribute",
			tokens: []string{"resources_max.walltime", "=", "01:00:00"},
			want:   []dis.SvrAttrl{{Name: "resources_max", HasResc: true, Resc: "walltime", Value: "01:00:00", Op: 1}},
		},
		{
			name:   "multiple whitespace-separated attrs",
			tokens: []string{"max_queuable", "=", "5", "max_running", "=", "10"},
			want: []dis.SvrAttrl{
				{Name: "max_queuable", Value: "5", Op: 1},
				{Name: "max_running", Value: "10", Op: 1},
			},
		},
		{
			name:   "contiguous key=value with comma",
			tokens: []string{"acl_users=bob,alice"},
			want:   []dis.SvrAttrl{{Name: "acl_users", Value: "bob,alice", Op: 1}},
		},
		{
			name:   "attached operator increments",
			tokens: []string{"acl_users+=bob"},
			want:   []dis.SvrAttrl{{Name: "acl_users", Value: "bob", Op: 7}},
		},
		{
			name:   "bare key",
			tokens: []string{"enabled"},
			want:   []dis.SvrAttrl{{Name: "enabled", Value: "", Op: 1}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAttrs(tc.tokens)
			if len(got) != len(tc.want) {
				t.Fatalf("parseAttrs(%v) = %d attrs, want %d: %+v", tc.tokens, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("attr[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
