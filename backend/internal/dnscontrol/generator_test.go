package dnscontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ALLinSSL/backend/internal/dnsmodel"
)

func TestGenerateArtifactsUsesCompatibleFullSnapshot(t *testing.T) {
	snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{
		{Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"},
		{Name: "_acme-challenge", Type: "TXT", TTL: 60, Value: "token", Line: "default", Status: "ENABLE"},
	}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}

	artifacts, err := GenerateArtifacts(snapshot, AliDNSCredentials{AccessKeyID: "key-id", AccessKeySecret: "key-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(artifacts.Config, []byte(`A("www", "192.0.2.10")`)) {
		t.Fatal("A record missing")
	}
	if bytes.Contains(artifacts.Config, []byte("key-secret")) || bytes.Contains(artifacts.Config, []byte("token")) {
		t.Fatal("unsafe config content")
	}
	if !bytes.Contains(artifacts.Credentials, []byte("key-secret")) {
		t.Fatal("credential missing")
	}
}

func TestGenerateArtifactsIsDeterministicAndProtectsReservedRecords(t *testing.T) {
	priority, weight, port, flags := int64(10), int64(20), int64(443), int64(0)
	records := []dnsmodel.Record{
		{Name: "z", Type: "TXT", TTL: 600, Value: "quoted \"text\"", Line: "default", Status: "ENABLE"},
		{Name: "mail", Type: "MX", TTL: 600, Value: "mail.example.net.", Priority: &priority, Line: "default", Status: "ENABLE"},
		{Name: "_sip._tcp", Type: "SRV", TTL: 600, Value: "service.example.net.", Priority: &priority, Weight: &weight, Port: &port, Line: "default", Status: "ENABLE"},
		{Name: "caa", Type: "CAA", TTL: 600, Value: "letsencrypt.org", CAAFlags: &flags, CAATag: "issue", Line: "default", Status: "ENABLE"},
		{Name: "@", Type: "NS", TTL: 600, Value: "ns1.example.net.", Line: "default", Status: "ENABLE"},
		{Name: "_acme-challenge.sub", Type: "CNAME", TTL: 600, Value: "delegate.example.net.", Line: "default", Status: "ENABLE"},
	}
	snapshot, err := dnsmodel.BuildSnapshot("example.com", records, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	credentials := AliDNSCredentials{AccessKeyID: "key-id", AccessKeySecret: "key-secret"}
	first, err := GenerateArtifacts(snapshot, credentials)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifacts(snapshot, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Config, second.Config) || !bytes.Equal(first.Credentials, second.Credentials) {
		t.Fatal("same input generated different artifacts")
	}
	for _, want := range []string{
		`IGNORE("_acme-challenge", "*")`,
		`IGNORE("_acme-challenge.**", "*")`,
		`IGNORE("@", "NS,SOA")`,
		`DefaultTTL(600)`,
		`MX("mail", 10, "mail.example.net.")`,
		`SRV("_sip._tcp", 10, 20, 443, "service.example.net.")`,
		`CAA("caa", "issue", "letsencrypt.org")`,
	} {
		if !bytes.Contains(first.Config, []byte(want)) {
			t.Fatalf("config missing %q:\n%s", want, first.Config)
		}
	}
	if bytes.Contains(first.Config, []byte("delegate.example.net")) || bytes.Contains(first.Config, []byte("key-secret")) {
		t.Fatalf("config leaked protected record or credential:\n%s", first.Config)
	}
}

func TestGenerateArtifactsRejectsUnsafeInputs(t *testing.T) {
	compatibleSnapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{
		{Name: "www", Type: "A", TTL: 600, Value: "192.0.2.10", Line: "default", Status: "ENABLE"},
	}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name        string
		snapshot    dnsmodel.Snapshot
		credentials AliDNSCredentials
		want        error
	}{
		{"incompatible snapshot", dnsmodel.Snapshot{}, AliDNSCredentials{AccessKeyID: "id", AccessKeySecret: "secret"}, ErrIncompatibleSnapshot},
		{"noncanonical zone", func() dnsmodel.Snapshot {
			snapshot := compatibleSnapshot
			snapshot.Zone = "Example.com."
			return snapshot
		}(), AliDNSCredentials{AccessKeyID: "id", AccessKeySecret: "secret"}, ErrInvalidZone},
		{"missing access key id", compatibleSnapshot, AliDNSCredentials{AccessKeySecret: "secret"}, ErrInvalidCredentials},
		{"missing access key secret", compatibleSnapshot, AliDNSCredentials{AccessKeyID: "id"}, ErrInvalidCredentials},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := GenerateArtifacts(testCase.snapshot, testCase.credentials)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("GenerateArtifacts error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestGenerateArtifactsPreservesAllCAAFlags(t *testing.T) {
	flags := []int64{0, 1, 127, 128, 255}
	records := make([]dnsmodel.Record, 0, len(flags))
	for _, flag := range flags {
		flagText := strconv.FormatInt(flag, 10)
		records = append(records, dnsmodel.Record{
			Name:     "caa-" + flagText,
			Type:     "CAA",
			TTL:      600,
			Value:    "flag-" + flagText,
			CAAFlags: int64Pointer(flag),
			CAATag:   "issue",
			Line:     "default",
			Status:   "ENABLE",
		})
	}
	snapshot, err := dnsmodel.BuildSnapshot("example.com", records, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}

	artifacts, err := GenerateArtifacts(snapshot, AliDNSCredentials{AccessKeyID: "key-id", AccessKeySecret: "key-secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range flags {
		flagText := strconv.FormatInt(flag, 10)
		want := `CAA("caa-` + flagText + `", "issue", "flag-` + flagText + `"`
		if flag != 0 {
			want += `, CAA_FLAGS(` + flagText + `)`
		}
		want += `)`
		if !bytes.Contains(artifacts.Config, []byte(want)) {
			t.Fatalf("config missing %q:\n%s", want, artifacts.Config)
		}
	}
}

func TestJavaScriptStringLiteralRoundTripsAllSupportedCharacters(t *testing.T) {
	for _, value := range []string{
		string([]byte{0x01, 0x07, 0x0b, 0x1f}) + `\\"` + "\u2028雪",
		"plain Unicode: 中文",
	} {
		literal := quote(value)
		var roundTripped string
		if err := json.Unmarshal([]byte(literal), &roundTripped); err != nil {
			t.Fatalf("literal %q is not JSON: %v", literal, err)
		}
		if roundTripped != value {
			t.Fatalf("round trip = %q, want %q", roundTripped, value)
		}
	}
}

func TestGeneratedArtifactsPassDNSControlCheckWithEscapedStringsAndAllCAAFlags(t *testing.T) {
	binary, err := exec.LookPath("dnscontrol-5.0.3")
	if err != nil {
		t.Skip("dnscontrol-5.0.3 is not installed")
	}
	flag := int64(255)
	snapshot, err := dnsmodel.BuildSnapshot("example.com", []dnsmodel.Record{
		{Name: "txt", Type: "TXT", TTL: 600, Value: string([]byte{0x01, 0x07, 0x0b, 0x1f}) + `\\"` + "\u2028雪", Line: "default", Status: "ENABLE"},
		{Name: "caa", Type: "CAA", TTL: 600, Value: string([]byte{0x07}) + `\\"` + "\u2028雪", CAAFlags: &flag, CAATag: "issue", Line: "default", Status: "ENABLE"},
	}, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := GenerateArtifacts(snapshot, AliDNSCredentials{AccessKeyID: "test-key-id", AccessKeySecret: "test-key-secret"})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "dnsconfig.js")
	if err := os.WriteFile(configPath, artifacts.Config, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "check", "--config", configPath).CombinedOutput(); err != nil {
		t.Fatalf("dnscontrol check: %v\n%s", err, output)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestGenerateArtifactsCredentialsContainOnlyAliDNSAuthorization(t *testing.T) {
	snapshot, err := dnsmodel.BuildSnapshot("example.com", nil, dnsmodel.Limits{MinTTL: 600, MaxTTL: 86400, Known: true})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := GenerateArtifacts(snapshot, AliDNSCredentials{AccessKeyID: "key-id", AccessKeySecret: "key-secret"})
	if err != nil {
		t.Fatal(err)
	}

	var credentials map[string]map[string]string
	if err := json.Unmarshal(artifacts.Credentials, &credentials); err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("credentials = %#v", credentials)
	}
	if got := credentials["alidns"]; got["TYPE"] != "ALIDNS" || got["access_key_id"] != "key-id" || got["access_key_secret"] != "key-secret" || got["region_id"] != "cn-hangzhou" || len(got) != 4 {
		t.Fatalf("alidns credentials = %#v", got)
	}
	if strings.Contains(string(artifacts.Config), "key-id") || strings.Contains(string(artifacts.Config), "key-secret") {
		t.Fatalf("config leaked credentials:\n%s", artifacts.Config)
	}
}
