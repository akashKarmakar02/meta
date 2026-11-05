//go:build imageproc_native

package main

import "testing"

func TestF06ConfiguredImageLimitsFailClosed(t *testing.T) {
	const key = "GO_META_F06_TEST_LIMIT"
	for _, v := range []string{"", "0", "-1", "65", "not-a-number"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(key, v)
			if _, err := envImageLimit(key, 4, 64); err == nil {
				t.Fatalf("accepted %q", v)
			}
		})
	}
	t.Setenv(key, "64")
	if got, err := envImageLimit(key, 4, 64); err != nil || got != 64 {
		t.Fatalf("valid limit %d %v", got, err)
	}
}
