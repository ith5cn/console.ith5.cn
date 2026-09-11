package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRegistryText(t *testing.T) {
	r := New()
	r.Observe("GET", "/v1/me", 200, 3*time.Millisecond)
	r.Observe("GET", "/v1/me", 200, 30*time.Millisecond)
	r.Observe("POST", "/v1/change-sets", 422, 2*time.Second)
	var sb strings.Builder
	r.Dump(&sb)
	out := sb.String()
	for _, want := range []string{
		`ith5_http_requests_total{method="GET",route="/v1/me",status="200"} 2`,
		`ith5_http_requests_total{method="POST",route="/v1/change-sets",status="422"} 1`,
		`ith5_http_request_duration_seconds_bucket{method="GET",route="/v1/me",le="0.005"} 1`,
		`ith5_http_request_duration_seconds_bucket{method="GET",route="/v1/me",le="+Inf"} 2`,
		`ith5_http_request_duration_seconds_count{method="POST",route="/v1/change-sets"} 1`,
		"ith5_up 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q\n%s", want, out)
		}
	}
}
