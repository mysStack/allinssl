package dnsmodel

import (
	"reflect"
	"strings"
	"testing"
)

func intp(n int64) *int64 { return &n }
func aRecord() Record { return Record{ProviderRecordID:"1", Name:"www", Type:"A", TTL:600, Value:"192.0.2.10", Line:"default", Status:"ENABLE"} }
func snapshot(t *testing.T, records ...Record) Snapshot {
	t.Helper()
	s, err := BuildSnapshot("example.com", records, Limits{600,86400,true})
	if err != nil { t.Fatal(err) }
	if len(s.Records)!=len(records) {t.Fatalf("snapshot lost records: got %d, want %d",len(s.Records),len(records))}
	return s
}

func TestZoneNormalizationAndScope(t *testing.T) {
	for _, tt := range []struct{in,want string}{ {" EXAMPLE.COM. ","example.com"}, {"例子.中国","xn--fsqu00a.xn--fiqs8s"} } {
		got,err:=NormalizeZone(tt.in); if err!=nil || got!=tt.want { t.Errorf("%q: got %q, %v; want %q",tt.in,got,err,tt.want) }
	}
	for _,bad:=range []string{"",".","*.example.com","foo..com","example.com/path","_acme.example.com","-bad.example"} {
		if _,err:=NormalizeZone(bad);err==nil {t.Errorf("accepted invalid zone %q",bad)}
	}
	r:=aRecord();r.Name="www.other.com."
	if _,err:=BuildSnapshot("example.com",[]Record{r},Limits{600,86400,true});err==nil {t.Fatal("accepted out-of-zone absolute name")}
}

func TestHashesSeparateRecordIdentityAndProtectedInventory(t *testing.T) {
	a:=aRecord(); challenge:=Record{ProviderRecordID:"acme-1",Name:"_acme-challenge.a.b",Type:"TXT",TTL:60,Value:"token-one",Line:"default",Status:"ENABLE"}
	base:=snapshot(t,a,challenge)
	if !base.Compatible || base.WriteEnabled || len(base.SnapshotHash)!=64 {t.Fatalf("unexpected eligibility/hash: %+v",base)}
	reordered:=snapshot(t,challenge,a)
	if base.SnapshotHash!=reordered.SnapshotHash || base.InventoryHash!=reordered.InventoryHash {t.Fatal("return order changed hash")}
	challenge.Value="token-two";challenge.ProviderRecordID="acme-2"
	dynamic:=snapshot(t,a,challenge)
	if base.SnapshotHash!=dynamic.SnapshotHash || base.BusinessIdentityHash!=dynamic.BusinessIdentityHash || base.InventoryHash==dynamic.InventoryHash {t.Fatal("ACME change affected business hash or escaped inventory")}
	a.ProviderRecordID="recreated"
	recreated:=snapshot(t,a,challenge)
	if dynamic.SnapshotHash!=recreated.SnapshotHash || dynamic.BusinessIdentityHash==recreated.BusinessIdentityHash {t.Fatal("semantic and identity hashes conflated")}
	a.TTL=1200
	if snapshot(t,a,challenge).SnapshotHash==recreated.SnapshotHash {t.Fatal("TTL drift not detected")}
}

func TestPreservesTXTAndNormalizesTypedRDATA(t *testing.T) {
	text:="  MiXeD \"quote\" \\ newline\n文本  "
	records:=[]Record{
		{Name:"txt",Type:"TXT",Value:text,TTL:600,Line:"default",Status:"ENABLE"},
		{Name:"mail",Type:"MX",Value:"MAIL.Example.NET",Priority:intp(10),TTL:600,Line:"default",Status:"ENABLE"},
		{Name:"_sip._tcp",Type:"SRV",Value:"10 20 443 SERVICE.Example.NET.",TTL:600,Line:"default",Status:"ENABLE"},
		{Name:"caa",Type:"CAA",Value:`0 issue "letsencrypt.org"`,TTL:600,Line:"default",Status:"ENABLE"},
	}
	s:=snapshot(t,records...)
	if !s.Compatible {t.Fatalf("ordinary records rejected: %+v",s)}
	byName:=map[string]Record{};for _,r:=range s.Records {byName[r.Name]=r}
	if byName["txt"].Value!=text || byName["mail"].Value!="mail.example.net." {t.Fatal("TXT altered or MX target not normalized")}
	srv:=byName["_sip._tcp"];if srv.Value!="service.example.net." || srv.Port==nil || *srv.Port!=443 || srv.Weight==nil || *srv.Weight!=20 || srv.Priority==nil || *srv.Priority!=10 {t.Fatalf("SRV lost fields: %+v",srv)}
	caa:=byName["caa"];if caa.CAAFlags==nil || *caa.CAAFlags!=0 || caa.CAATag!="issue" || caa.Value!="letsencrypt.org" {t.Fatalf("CAA lost fields: %+v",caa)}
	if records[1].Value!="MAIL.Example.NET" {t.Fatal("mutated caller records")}
}

func TestIncompatibleRecordsRemainVisibleAndBlockWholeZone(t *testing.T) {
	for _,tt:=range []struct{name string;modify func(*Record)}{
		{"disabled",func(r *Record){r.Status="DISABLE"}},
		{"line",func(r *Record){r.Line="telecom"}},
		{"unknown-type",func(r *Record){r.Type="HTTPS";r.Value="1 ."}},
		{"metadata",func(r *Record){r.Metadata=map[string]string{"unknown":"present"}}},
		{"bad-ip",func(r *Record){r.Value="not-an-ip"}},
		{"ttl",func(r *Record){r.TTL=100}},
		{"bad-name",func(r *Record){r.Name="bad;name"}},
	} {t.Run(tt.name,func(t *testing.T){r:=aRecord();tt.modify(&r);s:=snapshot(t,r);if s.Compatible || len(s.Records)!=1 || len(s.ReadOnlyReasons)==0 {t.Fatalf("unsafe record accepted/dropped: %+v",s)}})}
	unknown,err:=BuildSnapshot("example.com",[]Record{aRecord()},Limits{})
	if err!=nil || unknown.Compatible {t.Fatal("unknown TTL capability accepted")}
}

func TestProtectedNamesDoNotProtectLookalikes(t *testing.T) {
	for _,tt:=range []struct{name,typ string;protected bool}{
		{"_acme-challenge","TXT",true},{"_acme-challenge.a.b","CNAME",true},
		{"@","NS",true},{"@","SOA",true},{"_acme-challenge-evil","TXT",false},{"foo._acme-challenge","TXT",false},
	} {r:=aRecord();r.Name=tt.name;r.Type=tt.typ;r.Value="fixture";s:=snapshot(t,r);if s.Records[0].Protected!=tt.protected {t.Errorf("%s %s protection mismatch",tt.name,tt.typ)}}
}

func TestDuplicatesCNAMEConflictsAndHashIsolation(t *testing.T) {
	a:=aRecord();dup:=a;dup.ProviderRecordID="2"
	if snapshot(t,a,dup).Compatible {t.Fatal("duplicate records accepted")}
	cn:=a;cn.ProviderRecordID="3";cn.Type="CNAME";cn.Value="target.example.net."
	if snapshot(t,a,cn).Compatible {t.Fatal("CNAME conflict accepted")}
	r:=a;r.Metadata=map[string]string{"remark":"keep"}
	s:=snapshot(t,r);s.Records[0].Metadata["remark"]="changed"
	if !reflect.DeepEqual(r.Metadata,map[string]string{"remark":"keep"}) {t.Fatal("snapshot aliases caller metadata")}
	if strings.Contains(s.SnapshotHash,"remark") {t.Fatal("hash exposed content")}
}

func TestUnrepresentableTypedFieldsBlockAdoption(t *testing.T) {
	for _,r:=range []Record{
		{Name:"www",Type:"A",Value:"192.0.2.1",Priority:intp(10)},
		{Name:"www",Type:"SRV",Value:"0 0 443 target.example.net."},
		{Name:"caa",Type:"CAA",Value:"0 issue \"ca.example\"\nextra.example. IN A 192.0.2.1"},
		{Name:"_acme-challenge.bad;name",Type:"TXT",Value:"token"},
	} {r.TTL=600;r.Line="default";r.Status="ENABLE";if snapshot(t,r).Compatible {t.Errorf("unrepresentable record accepted: %s %s",r.Name,r.Type)}}
	null:=Record{Name:"@",Type:"MX",Value:".",Priority:intp(0),TTL:600,Line:"default",Status:"ENABLE"}
	if !snapshot(t,null).Compatible {t.Fatal("standalone null MX rejected")}
	mail:=null;mail.Value="mail.example.net.";mail.Priority=intp(10)
	if snapshot(t,null,mail).Compatible {t.Fatal("null MX mixed with a mail server")}
}
