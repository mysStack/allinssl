package api

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/internal/dnsmodel"
	"ALLinSSL/backend/public"
	"github.com/gin-gonic/gin"
)

type DNSReaderService interface {
	ListCredentials(context.Context) ([]dns.CredentialSummary, error)
	ListZones(context.Context, int64) ([]dns.ZoneSummary, error)
	ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error)
}

type DNSHandler struct {
	service DNSReaderService
}

func NewDNSHandler(service DNSReaderService) DNSHandler {
	return DNSHandler{service: service}
}

func DefaultDNSService() DNSReaderService {
	database, err := sql.Open("sqlite", "data/data.db")
	if err != nil {
		return unavailableDNSService{err: err}
	}
	return dns.NewSQLService(database)
}

func DefaultDNSHandler() DNSHandler {
	database, err := sql.Open("sqlite", "data/data.db")
	if err != nil {
		return NewDNSHandler(unavailableDNSService{err: err})
	}
	return NewDNSHandler(dns.NewSQLService(database))
}

type unavailableDNSService struct{ err error }

func (s unavailableDNSService) ListCredentials(context.Context) ([]dns.CredentialSummary, error) {
	return nil, s.error()
}

func (s unavailableDNSService) ListZones(context.Context, int64) ([]dns.ZoneSummary, error) {
	return nil, s.error()
}

func (s unavailableDNSService) ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error) {
	return dnsmodel.Snapshot{}, s.error()
}

func (s unavailableDNSService) error() error {
	if s.err != nil {
		return s.err
	}
	return errors.New("DNS service unavailable")
}

func (h DNSHandler) GetCredentials(c *gin.Context) {
	if h.service == nil {
		public.FailMsg(c, "DNS 读取服务不可用")
		return
	}
	credentials, err := h.service.ListCredentials(c.Request.Context())
	if err != nil {
		public.FailMsg(c, "DNS 授权读取失败")
		return
	}
	public.SuccessData(c, credentials, len(credentials))
}

func (h DNSHandler) GetZones(c *gin.Context) {
	credentialID, ok := dnsCredentialID(c)
	if !ok {
		return
	}
	if h.service == nil {
		public.FailMsg(c, "DNS 读取服务不可用")
		return
	}
	zones, err := h.service.ListZones(c.Request.Context(), credentialID)
	if err != nil {
		public.FailMsg(c, "DNS Zone 读取失败")
		return
	}
	public.SuccessData(c, zones, len(zones))
}

func (h DNSHandler) GetSnapshot(c *gin.Context) {
	credentialID, ok := dnsCredentialID(c)
	if !ok {
		return
	}
	zone := strings.TrimSpace(c.PostForm("zone"))
	if zone == "" {
		public.FailMsg(c, "zone 不能为空")
		return
	}
	if h.service == nil {
		public.FailMsg(c, "DNS 读取服务不可用")
		return
	}
	snapshot, err := h.service.ReadZone(c.Request.Context(), credentialID, zone)
	if err != nil {
		public.FailMsg(c, "DNS 记录读取失败")
		return
	}
	public.SuccessData(c, snapshot, len(snapshot.Records))
}

func dnsCredentialID(c *gin.Context) (int64, bool) {
	credentialID, err := strconv.ParseInt(strings.TrimSpace(c.PostForm("credential_id")), 10, 64)
	if err != nil || credentialID <= 0 {
		public.FailMsg(c, "credential_id 必须为正整数")
		return 0, false
	}
	return credentialID, true
}
