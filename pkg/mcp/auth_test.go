package mcp

import (
	"net/http"
	"testing"
)

func TestBearerToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{
			name:   "case-insensitive scheme and surrounding spaces",
			values: []string{"bEaReR   opaque-token  "},
			want:   "opaque-token",
		},
		{
			name: "missing header",
		},
		{
			name:   "missing token",
			values: []string{"Bearer"},
		},
		{
			name:   "blank token",
			values: []string{"Bearer   "},
		},
		{
			name:   "wrong scheme",
			values: []string{"Basic opaque-token"},
		},
		{
			name:   "first token valid",
			values: []string{"Bearer opaque-token", "Bearer invalid"},
		},
		{
			name:   "second token valid",
			values: []string{"Bearer invalid", "Bearer opaque-token"},
		},
		{
			name:   "duplicate token",
			values: []string{"Bearer opaque-token", "Bearer opaque-token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bearerToken(http.Header{"Authorization": tc.values}); got != tc.want {
				t.Fatalf("bearerToken = %q, want %q", got, tc.want)
			}
		})
	}
}
