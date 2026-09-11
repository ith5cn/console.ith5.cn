package api

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0}, {"0.0.9", "0.1.0", -1}, {"1.0", "0.9.9", 1},
		{"v0.2.0-beta", "0.1.0", 1}, {"0.1", "0.1.0", 0}, {"garbage", "0.1.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
