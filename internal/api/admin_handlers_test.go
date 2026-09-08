package api

import (
	"net/http"
	"testing"
)

func TestDemoMutationBlocked(t *testing.T) {
	tests := []struct {
		role    string
		method  string
		blocked bool
	}{
		{"viewer", http.MethodGet, false},
		{"viewer", http.MethodHead, false},
		{"viewer", http.MethodOptions, false},
		{"viewer", http.MethodPost, true},
		{"viewer", http.MethodPut, true},
		{"viewer", http.MethodDelete, true},
		{"owner", http.MethodPost, false},
		{"admin", http.MethodDelete, false},
		{"member", http.MethodPost, false},
	}

	for _, tt := range tests {
		t.Run(tt.role+"_"+tt.method, func(t *testing.T) {
			if got := demoMutationBlocked(tt.role, tt.method); got != tt.blocked {
				t.Fatalf("demoMutationBlocked(%q, %q) = %v, want %v", tt.role, tt.method, got, tt.blocked)
			}
		})
	}
}
