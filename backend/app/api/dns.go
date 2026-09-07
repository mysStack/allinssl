package api

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"ALLinSSL/backend/internal/dns"
	"ALLinSSL/backend/internal/dnscontrol"
	"ALLinSSL/backend/internal/dnsmodel"
	"ALLinSSL/backend/middleware"
	"ALLinSSL/backend/public"
	"github.com/gin-gonic/gin"
)

type DNSReaderService interface {
	ListCredentials(context.Context) ([]dns.CredentialSummary, error)
	ListZones(context.Context, int64) ([]dns.ZoneSummary, error)
	ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error)
}

type DNSAdoptService interface {
	Health(context.Context) (dnscontrol.EngineInfo, error)
	Start(context.Context, dns.AdoptStartInput) (dns.AdoptJob, error)
	StartCreateRecordPreview(context.Context, dns.CreateRecordPreviewInput) (dns.AdoptJob, error)
	GetJob(context.Context, string, dns.SessionIdentity) (dns.AdoptJob, error)
}

type DNSHandler struct {
	service DNSReaderService
	adopt   DNSAdoptService
}

func NewDNSHandler(service DNSReaderService, adopt ...DNSAdoptService) DNSHandler {
	handler := DNSHandler{service: service}
	if len(adopt) > 0 {
		handler.adopt = adopt[0]
	}
	return handler
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
	reader := dns.NewSQLService(database)
	adopt, err := dns.NewSQLAdoptService(database)
	if err != nil {
		return NewDNSHandler(reader)
	}
	_ = adopt.RecoverInterrupted(context.Background())
	return NewDNSHandler(reader, adopt)
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

func (h DNSHandler) GetHealth(c *gin.Context) {
	identity, ok := middleware.DNSIdentity(c)
	if !ok {
		public.FailMsg(c, "DNS 会话无效")
		return
	}
	response := dnsHealthResponse{CSRFToken: identity.CSRFToken}
	if h.adopt != nil {
		info, err := h.adopt.Health(c.Request.Context())
		if err == nil {
			response.Version = info.Version
			response.PreviewAvailable = info.Version == dnscontrol.ExpectedVersion
		}
	}
	public.SuccessData(c, response, 1)
}

func (h DNSHandler) BindZone(c *gin.Context) {
	identity, ok := middleware.DNSIdentity(c)
	if !ok || h.adopt == nil {
		public.FailMsg(c, "DNS 纳管服务不可用")
		return
	}
	form, ok := bindZoneForm(c)
	if !ok {
		return
	}
	job, err := h.adopt.Start(c.Request.Context(), dns.AdoptStartInput{
		Identity: identity, CredentialID: form.credentialID, Zone: form.zone, SnapshotHash: form.snapshotHash,
		IdempotencyKey: form.idempotencyKey, AdoptAll: true,
	})
	if err != nil {
		public.FailMsg(c, "DNS 纳管预览创建失败")
		return
	}
	public.SuccessData(c, gin.H{"job_id": job.ID, "state": job.State}, 1)
}

func (h DNSHandler) CreateRecordPreview(c *gin.Context) {
	identity, ok := middleware.DNSIdentity(c)
	if !ok || h.adopt == nil {
		public.FailMsg(c, "DNS 记录预览服务不可用")
		return
	}
	form, ok := createRecordPreviewForm(c)
	if !ok {
		return
	}
	job, err := h.adopt.StartCreateRecordPreview(c.Request.Context(), dns.CreateRecordPreviewInput{
		Identity:         identity,
		CredentialID:     form.credentialID,
		Zone:             form.zone,
		BaseSnapshotHash: form.snapshotHash,
		Record:           form.record,
		IdempotencyKey:   form.idempotencyKey,
	})
	if err != nil {
		public.FailMsg(c, "DNS 记录预览创建失败")
		return
	}
	public.SuccessData(c, gin.H{"job_id": job.ID, "state": job.State}, 1)
}

func (h DNSHandler) GetJob(c *gin.Context) {
	identity, ok := middleware.DNSIdentity(c)
	if !ok || h.adopt == nil {
		public.FailMsg(c, "DNS 纳管服务不可用")
		return
	}
	jobID, ok := strictDNSFormValue(c, map[string]bool{"job_id": true}, "job_id")
	if !ok {
		return
	}
	job, err := h.adopt.GetJob(c.Request.Context(), jobID, identity)
	if err != nil {
		public.FailMsg(c, "DNS 纳管任务不存在或无权访问")
		return
	}
	var candidateRecord *dns.CandidateRecordSummary
	if job.Kind == dns.JobKindCreateRecordPreview && job.CandidateRecord != (dns.CandidateRecordSummary{}) {
		candidate := job.CandidateRecord
		candidateRecord = &candidate
	}
	public.SuccessData(c, dnsJobResponse{
		ID: job.ID, Kind: job.Kind, Zone: job.Zone, CredentialID: job.CredentialID, State: job.State,
		SnapshotHash: job.SnapshotHash, PlanHash: job.PlanHash, ErrorCode: job.ErrorCode,
		CandidateRecord: candidateRecord, ChangeSummary: safeDNSChangeSummary(job.ChangeSummary),
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}, 1)
}

func safeDNSChangeSummary(summary dns.ChangeSummary) *dns.ChangeSummary {
	if summary.Corrections <= 0 || len(summary.Details) == 0 {
		return nil
	}
	for _, detail := range summary.Details {
		if len(detail) != len("sha256:")+64 || !strings.HasPrefix(detail, "sha256:") {
			return nil
		}
		for _, character := range detail[len("sha256:"):] {
			if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
				return nil
			}
		}
	}
	safe := dns.ChangeSummary{Corrections: summary.Corrections, Details: append([]string(nil), summary.Details...)}
	return &safe
}

type dnsHealthResponse struct {
	Version          string `json:"version"`
	PreviewAvailable bool   `json:"preview_available"`
	CSRFToken        string `json:"csrf_token"`
}

type dnsJobResponse struct {
	ID              string                      `json:"id"`
	Kind            dns.JobKind                 `json:"kind"`
	Zone            string                      `json:"zone"`
	CredentialID    int64                       `json:"credential_id"`
	State           dns.JobState                `json:"state"`
	SnapshotHash    string                      `json:"snapshot_hash"`
	PlanHash        string                      `json:"plan_hash,omitempty"`
	CandidateRecord *dns.CandidateRecordSummary `json:"candidate_record,omitempty"`
	ChangeSummary   *dns.ChangeSummary          `json:"change_summary,omitempty"`
	ErrorCode       string                      `json:"error_code,omitempty"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
}

type bindZoneRequest struct {
	credentialID   int64
	zone           string
	snapshotHash   string
	idempotencyKey string
}

type createRecordPreviewRequest struct {
	credentialID   int64
	zone           string
	snapshotHash   string
	record         dns.CreateRecordInput
	idempotencyKey string
}

func createRecordPreviewForm(c *gin.Context) (createRecordPreviewRequest, bool) {
	const message = "DNS 记录预览请求参数无效"
	if c.Request.ParseForm() != nil || len(c.Request.URL.Query()) != 0 {
		public.FailMsg(c, message)
		return createRecordPreviewRequest{}, false
	}
	recordType := strings.TrimSpace(c.Request.PostForm.Get("type"))
	allowed := map[string]bool{
		"credential_id": true, "zone": true, "base_snapshot_hash": true,
		"name": true, "type": true, "ttl": true, "value": true,
		"idempotency_key": true, "csrf_token": true,
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
		return createRecordPreviewRequest{}, false
	}
	if !strictDNSForm(c.Request.PostForm, allowed) {
		public.FailMsg(c, message)
		return createRecordPreviewRequest{}, false
	}

	credentialID, credentialOK := strictDecimal(c.Request.PostForm.Get("credential_id"), 1, int64(^uint64(0)>>1))
	ttl, ttlOK := strictDecimal(c.Request.PostForm.Get("ttl"), 600, 86400)
	request := createRecordPreviewRequest{
		credentialID: credentialID,
		zone:         strings.TrimSpace(c.Request.PostForm.Get("zone")),
		snapshotHash: strings.TrimSpace(c.Request.PostForm.Get("base_snapshot_hash")),
		record: dns.CreateRecordInput{
			Name: strings.TrimSpace(c.Request.PostForm.Get("name")), Type: recordType,
			TTL: ttl, Value: c.Request.PostForm.Get("value"),
		},
		idempotencyKey: strings.TrimSpace(c.Request.PostForm.Get("idempotency_key")),
	}
	valid := credentialOK && ttlOK && request.zone != "" && request.snapshotHash != "" &&
		request.record.Name != "" && request.record.Value != "" && request.idempotencyKey != "" && c.Request.PostForm.Get("csrf_token") != ""
	switch recordType {
	case "MX":
		priority, ok := strictDecimal(c.Request.PostForm.Get("priority"), 0, 65535)
		request.record.Priority = &priority
		valid = valid && ok
	case "SRV":
		priority, priorityOK := strictDecimal(c.Request.PostForm.Get("priority"), 0, 65535)
		weight, weightOK := strictDecimal(c.Request.PostForm.Get("weight"), 0, 65535)
		port, portOK := strictDecimal(c.Request.PostForm.Get("port"), 0, 65535)
		request.record.Priority = &priority
		request.record.Weight = &weight
		request.record.Port = &port
		valid = valid && priorityOK && weightOK && portOK
	case "CAA":
		flags, ok := strictDecimal(c.Request.PostForm.Get("caa_flags"), 0, 255)
		request.record.CAAFlags = &flags
		request.record.CAATag = c.Request.PostForm.Get("caa_tag")
		valid = valid && ok && request.record.CAATag != ""
	}
	if !valid {
		public.FailMsg(c, message)
		return createRecordPreviewRequest{}, false
	}
	return request, true
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

func bindZoneForm(c *gin.Context) (bindZoneRequest, bool) {
	const message = "DNS 纳管请求参数无效"
	if c.Request.ParseForm() != nil {
		public.FailMsg(c, message)
		return bindZoneRequest{}, false
	}
	allowed := map[string]bool{
		"credential_id": true, "zone": true, "snapshot_hash": true, "adopt_all": true,
		"idempotency_key": true, "csrf_token": true,
	}
	if !strictDNSForm(c.Request.PostForm, allowed) {
		public.FailMsg(c, message)
		return bindZoneRequest{}, false
	}
	credentialID, err := strconv.ParseInt(c.Request.PostForm.Get("credential_id"), 10, 64)
	request := bindZoneRequest{
		credentialID:   credentialID,
		zone:           strings.TrimSpace(c.Request.PostForm.Get("zone")),
		snapshotHash:   strings.TrimSpace(c.Request.PostForm.Get("snapshot_hash")),
		idempotencyKey: strings.TrimSpace(c.Request.PostForm.Get("idempotency_key")),
	}
	if err != nil || request.credentialID <= 0 || request.zone == "" || request.snapshotHash == "" || request.idempotencyKey == "" || c.Request.PostForm.Get("adopt_all") != "true" {
		public.FailMsg(c, message)
		return bindZoneRequest{}, false
	}
	return request, true
}

func strictDNSFormValue(c *gin.Context, allowed map[string]bool, field string) (string, bool) {
	if c.Request.ParseForm() != nil || !strictDNSForm(c.Request.PostForm, allowed) {
		public.FailMsg(c, "DNS 纳管请求参数无效")
		return "", false
	}
	value := strings.TrimSpace(c.Request.PostForm.Get(field))
	if value == "" {
		public.FailMsg(c, "DNS 纳管请求参数无效")
		return "", false
	}
	return value, true
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

func dnsCredentialID(c *gin.Context) (int64, bool) {
	credentialID, err := strconv.ParseInt(strings.TrimSpace(c.PostForm("credential_id")), 10, 64)
	if err != nil || credentialID <= 0 {
		public.FailMsg(c, "credential_id 必须为正整数")
		return 0, false
	}
	return credentialID, true
}
