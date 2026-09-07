package dnscontrol

import (
	"errors"
	"testing"
)

func TestParseReportAcceptsAliDNSPlanAndNoneRegistrar(t *testing.T) {
	plan, err := ParseReport([]byte(`[{"domain":"example.com","corrections":2,"correction_details":["change A","add TXT"],"provider":"alidns"},{"domain":"example.com","corrections":0,"registrar":"none"}]`), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Zone != "example.com" || plan.Provider != "ALIDNS" || plan.Corrections != 2 || len(plan.Details) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestParseReportRejectsMalformedOrWrongZone(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("{"),
		[]byte(`[{"domain":"other.example","provider":"ALIDNS","corrections":0,"correction_details":[]}]`),
		[]byte(`[{"domain":"example.com","provider":"OTHER","corrections":0,"correction_details":[]}]`),
		[]byte(`[]`),
		[]byte(`[{"domain":"example.com","provider":"ALIDNS","corrections":-1,"correction_details":[]}]`),
		[]byte(`[{"domain":"example.com","provider":"ALIDNS","corrections":0,"correction_details":[],"unexpected":true}]`),
		[]byte(`[{"domain":"example.com","provider":"ALIDNS","corrections":0,"correction_details":[]},{"domain":"example.com","provider":"ALIDNS","corrections":0,"correction_details":[]}]`),
		[]byte(`[{"domain":"example.com","provider":"ALIDNS","corrections":0,"correction_details":[]},{"domain":"example.com","registrar":"none","corrections":1}]`),
	} {
		if _, err := ParseReport(data, "example.com"); !errors.Is(err, ErrInvalidReport) {
			t.Fatalf("ParseReport(%s) error = %v, want %v", data, err, ErrInvalidReport)
		}
	}
}
