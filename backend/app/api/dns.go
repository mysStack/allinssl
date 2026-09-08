package api

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/internal/dnsmodel"
	"ALLinSSL/backend/middleware"
	"ALLinSSL/backend/public"
	"github.com/gin-gonic/gin"
)

type DNSReaderService interface {
	ListCredentials(context.Context) ([]dns.CredentialSummary, error)
	ListZones(context.Context, int64) ([]dns.ZoneSummary, error)
	ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error)
	CreateRecord(context.Context, int64, string, dns.RecordInput) (dnsmodel.Snapshot, error)
	UpdateRecord(context.Context, int64, string, string, dns.RecordInput) (dnsmodel.Snapshot, error)
	DeleteRecord(context.Context, int64, string, string) (dnsmodel.Snapshot, error)
	SetRecordStatus(context.Context, int64, string, string, string) (dnsmodel.Snapshot, error)
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

func (s unavailableDNSService) CreateRecord(context.Context, int64, string, dns.RecordInput) (dnsmodel.Snapshot, error) {
	return dnsmodel.Snapshot{}, s.error()
}

func (s unavailableDNSService) UpdateRecord(context.Context, int64, string, string, dns.RecordInput) (dnsmodel.Snapshot, error) {
	return dnsmodel.Snapshot{}, s.error()
}

func (s unavailableDNSService) DeleteRecord(context.Context, int64, string, string) (dnsmodel.Snapshot, error) {
	return dnsmodel.Snapshot{}, s.error()
}

func (s unavailableDNSService) SetRecordStatus(context.Context, int64, string, string, string) (dnsmodel.Snapshot, error) {
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

func (h DNSHandler) GetSession(c *gin.Context) {
	identity, ok := middleware.DNSIdentity(c)
	if !ok {
		public.FailMsg(c, "DNS 会话无效")
		return
	}
	public.SuccessData(c, gin.H{"csrf_token": identity.CSRFToken}, 1)
}

func (h DNSHandler) CreateRecord(c *gin.Context) {
	form, ok := recordForm(c, false)
	if !ok || h.service == nil {
		return
	}
	snapshot, err := h.service.CreateRecord(c.Request.Context(), form.credentialID, form.zone, form.record)
	if err != nil {
		public.FailMsg(c, "DNS 记录创建失败，请刷新记录确认实际状态")
		return
	}
	public.SuccessData(c, snapshot, len(snapshot.Records))
}

func (h DNSHandler) UpdateRecord(c *gin.Context) {
	form, ok := recordForm(c, true)
	if !ok || h.service == nil {
		return
	}
	snapshot, err := h.service.UpdateRecord(c.Request.Context(), form.credentialID, form.zone, form.recordID, form.record)
	if err != nil {
		public.FailMsg(c, "DNS 记录更新失败，请刷新记录确认实际状态")
		return
	}
	public.SuccessData(c, snapshot, len(snapshot.Records))
}

func (h DNSHandler) DeleteRecord(c *gin.Context) {
	credentialID, zone, recordID, ok := recordIDForm(c, "DNS 删除请求参数无效", false)
	if !ok || h.service == nil {
		return
	}
	snapshot, err := h.service.DeleteRecord(c.Request.Context(), credentialID, zone, recordID)
	if err != nil {
		public.FailMsg(c, "DNS 记录删除失败，请刷新记录确认实际状态")
		return
	}
	public.SuccessData(c, snapshot, len(snapshot.Records))
}

func (h DNSHandler) SetRecordStatus(c *gin.Context) {
	credentialID, zone, recordID, ok := recordIDForm(c, "DNS 状态请求参数无效", true)
	if !ok || h.service == nil {
		return
	}
	status := c.PostForm("status")
	if status != "ENABLE" && status != "DISABLE" {
		public.FailMsg(c, "DNS 状态请求参数无效")
		return
	}
	snapshot, err := h.service.SetRecordStatus(c.Request.Context(), credentialID, zone, recordID, status)
	if err != nil {
		public.FailMsg(c, "DNS 状态更新失败，请刷新记录确认实际状态")
		return
	}
	public.SuccessData(c, snapshot, len(snapshot.Records))
}

type dnsRecordForm struct {
	credentialID int64
	zone         string
	recordID     string
	record       dns.RecordInput
}

func recordForm(c *gin.Context, requiresID bool) (dnsRecordForm, bool) {
	const message = "DNS 记录请求参数无效"
	if c.Request.ParseForm() != nil || len(c.Request.URL.Query()) != 0 {
		public.FailMsg(c, message)
		return dnsRecordForm{}, false
	}
	recordType := strings.ToUpper(strings.TrimSpace(c.PostForm("type")))
	allowed := map[string]bool{"credential_id": true, "zone": true, "name": true, "type": true, "ttl": true, "value": true, "line": true, "csrf_token": true}
	if requiresID {
		allowed["record_id"] = true
	}
	switch recordType {
	case "A", "AAAA", "CNAME", "TXT":
	case "MX":
		allowed["priority"] = true
	case "SRV":
		allowed["priority"] = true
		allowed["weight"] = true
		allowed["port"] = true
	case "CAA":
		allowed["caa_flags"] = true
		allowed["caa_tag"] = true
	default:
		public.FailMsg(c, message)
		return dnsRecordForm{}, false
	}
	if !strictDNSForm(c.Request.PostForm, allowed) {
		public.FailMsg(c, message)
		return dnsRecordForm{}, false
	}
	credentialID, credentialOK := strictDecimal(c.PostForm("credential_id"), 1, int64(^uint64(0)>>1))
	ttl, ttlOK := strictDecimal(c.PostForm("ttl"), 600, 86400)
	form := dnsRecordForm{credentialID: credentialID, zone: strings.TrimSpace(c.PostForm("zone")), recordID: strings.TrimSpace(c.PostForm("record_id")), record: dns.RecordInput{Name: strings.TrimSpace(c.PostForm("name")), Type: recordType, TTL: ttl, Value: c.PostForm("value"), Line: strings.TrimSpace(c.PostForm("line"))}}
	valid := credentialOK && ttlOK && form.zone != "" && form.record.Name != "" && form.record.Value != "" && form.record.Line != "" && c.PostForm("csrf_token") != "" && (!requiresID || form.recordID != "")
	switch recordType {
	case "MX":
		value, ok := strictDecimal(c.PostForm("priority"), 0, 65535)
		form.record.Priority = &value
		valid = valid && ok
	case "SRV":
		priority, priorityOK := strictDecimal(c.PostForm("priority"), 0, 65535)
		weight, weightOK := strictDecimal(c.PostForm("weight"), 0, 65535)
		port, portOK := strictDecimal(c.PostForm("port"), 0, 65535)
		form.record.Priority, form.record.Weight, form.record.Port = &priority, &weight, &port
		valid = valid && priorityOK && weightOK && portOK
	case "CAA":
		flags, ok := strictDecimal(c.PostForm("caa_flags"), 0, 255)
		form.record.CAAFlags = &flags
		form.record.CAATag = strings.TrimSpace(c.PostForm("caa_tag"))
		valid = valid && ok && form.record.CAATag != ""
	}
	if !valid {
		public.FailMsg(c, message)
		return dnsRecordForm{}, false
	}
	return form, true
}

func recordIDForm(c *gin.Context, message string, allowStatus bool) (int64, string, string, bool) {
	allowed := map[string]bool{"credential_id": true, "zone": true, "record_id": true, "csrf_token": true}
	if allowStatus {
		allowed["status"] = true
	}
	if c.Request.ParseForm() != nil || len(c.Request.URL.Query()) != 0 || !strictDNSForm(c.Request.PostForm, allowed) {
		public.FailMsg(c, message)
		return 0, "", "", false
	}
	credentialID, ok := strictDecimal(c.PostForm("credential_id"), 1, int64(^uint64(0)>>1))
	zone, recordID := strings.TrimSpace(c.PostForm("zone")), strings.TrimSpace(c.PostForm("record_id"))
	if !ok || zone == "" || recordID == "" || c.PostForm("csrf_token") == "" {
		public.FailMsg(c, message)
		return 0, "", "", false
	}
	return credentialID, zone, recordID, true
}

func strictDNSForm(values map[string][]string, allowed map[string]bool) bool {
	for key, items := range values {
		if !allowed[key] || len(items) != 1 {
			return false
		}
	}
	for key := range allowed {
		if len(values[key]) != 1 {
			return false
		}
	}
	return true
}

func strictDecimal(value string, minimum, maximum int64) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil && number >= minimum && number <= maximum
}

func dnsCredentialID(c *gin.Context) (int64, bool) {
	credentialID, err := strconv.ParseInt(strings.TrimSpace(c.PostForm("credential_id")), 10, 64)
	if err != nil || credentialID <= 0 {
		public.FailMsg(c, "credential_id 必须为正整数")
		return 0, false
	}
	return credentialID, true
}
