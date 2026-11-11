package main

import "testing"

func TestRedactQueryHidesSecrets(t *testing.T) {
	cases := map[string]string{
		"/ping":                         "/ping",
		"/api/v1/sms?page=2&iccid=8984": "/api/v1/sms?page=2&iccid=8984",
		"/api/v1/modems/8984/ws?token=eyJhbGciOi.abc.d": "/api/v1/modems/8984/ws?token=***",
		"/oidc/callback?code=abc123&state=xyz":          "/oidc/callback?code=***&state=***",
		"/x?a=1&TOKEN=s3cret&b=2":                       "/x?a=1&TOKEN=***&b=2",
		"/x?token":                                      "/x?token=***",
	}
	for in, want := range cases {
		if got := redactQuery(in); got != want {
			t.Errorf("redactQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
