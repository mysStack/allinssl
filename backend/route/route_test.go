package route

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDNSRoutesDoNotExposeDNSControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router)

	for _, route := range router.Routes() {
		switch route.Path {
		case "/v1/dns/get_health", "/v1/dns/bind_zone", "/v1/dns/create_record_preview", "/v1/dns/get_job":
			t.Fatalf("obsolete DNSControl route remains: %s", route.Path)
		}
	}
}
