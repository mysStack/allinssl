package middleware

import "testing"

func TestDNSRoutesDoNotAllowAPITokenAuthentication(t *testing.T) {
	for _, path := range []string{
		"/v1/dns/get_credentials",
		"/v1/dns/get_zones",
		"/v1/dns/get_snapshot",
	} {
		if canUseAPIToken(path) {
			t.Fatalf("DNS route accepts API token authentication: %s", path)
		}
	}
	if !canUseAPIToken("/v1/access/get_list") {
		t.Fatal("existing non-DNS route unexpectedly rejects API token authentication")
	}
}
