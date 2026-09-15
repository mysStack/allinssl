// Package dnsmodel contains provider-neutral DNS read models.
package dnsmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/miekg/dns"
	"golang.org/x/net/idna"
)

type Record struct {
	ProviderRecordID string `json:"provider_record_id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type"`
	TTL int64 `json:"ttl"`
	Value string `json:"value"`
	Priority *int64 `json:"priority,omitempty"`
	Weight *int64 `json:"weight,omitempty"`
	Port *int64 `json:"port,omitempty"`
	CAAFlags *int64 `json:"caa_flags,omitempty"`
	CAATag string `json:"caa_tag,omitempty"`
	Remark string `json:"remark,omitempty"`
	LoadBalancingPolicy string `json:"load_balancing_policy,omitempty"`
	LoadBalancingWeight *int64 `json:"load_balancing_weight,omitempty"`
	Line string `json:"line"`
	Status string `json:"status"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Protected bool `json:"protected"`
	ReadOnlyReasons []string `json:"read_only_reasons,omitempty"`
}

type Limits struct {
	MinTTL int64 `json:"min_ttl"`
	MaxTTL int64 `json:"max_ttl"`
	Known bool `json:"known"`
}

type Snapshot struct {
	Zone string `json:"zone"`
	Records []Record `json:"records"`
	Limits Limits `json:"limits"`
	PolicyVersion string `json:"policy_version"`
	SnapshotHash string `json:"snapshot_hash"`
	BusinessIdentityHash string `json:"business_identity_hash"`
	InventoryHash string `json:"inventory_hash"`
	Compatible bool `json:"compatible"`
	WriteEnabled bool `json:"write_enabled"`
	ReadOnlyReasons []string `json:"read_only_reasons"`
}

// NormalizeZone accepts a Unicode domain, never a URL or a record label.
func NormalizeZone(zone string) (string, error) {
	zone = strings.TrimSuffix(strings.TrimSpace(zone), ".")
	name, err := idna.Lookup.ToASCII(zone)
	if err != nil || !validName(name, false) { return "", errors.New("invalid DNS zone") }
	return strings.ToLower(name), nil
}

func validName(name string, record bool) bool {
	if name=="" || len(name)>253 { return false }
	for i,label := range strings.Split(name,".") {
		if record && i==0 && label=="*" {continue}
		if len(label)==0 || len(label)>63 || label[0]=='-' || label[len(label)-1]=='-' {return false}
		for _,c := range strings.ToLower(label) {
			if (c>='a' && c<='z') || (c>='0' && c<='9') || c=='-' || (record && c=='_') {continue}
			return false
		}
	}
	return true
}

func normalizeOwner(zone, name string) (string,error) {
	if name=="@" {return name,nil}
	absolute:=strings.HasSuffix(name,".")
	name=strings.TrimSuffix(name,".")
	parts:=strings.Split(name,".")
	for i,label:=range parts {
		if label=="*" || strings.Contains(label,"_") {parts[i]=strings.ToLower(label);continue}
		ascii,err:=idna.Lookup.ToASCII(label);if err!=nil {return name,nil};parts[i]=strings.ToLower(ascii)
	}
	name=strings.Join(parts,".")
	if absolute {
		if name==zone {return "@",nil}
		if !strings.HasSuffix(name,"."+zone) {return "",errors.New("record is outside the requested zone")}
		name=strings.TrimSuffix(name,"."+zone)
	}
	return name,nil
}

func protected(name,typ string) bool {
	return name=="_acme-challenge" || strings.HasPrefix(name,"_acme-challenge.") || (name=="@" && (typ=="NS" || typ=="SOA"))
}

func copyInt(n *int64) *int64 {if n==nil{return nil};v:=*n;return &v}
func copyRecord(r Record) Record {
	r.Priority=copyInt(r.Priority);r.Weight=copyInt(r.Weight);r.Port=copyInt(r.Port);r.CAAFlags=copyInt(r.CAAFlags);r.LoadBalancingWeight=copyInt(r.LoadBalancingWeight)
	if len(r.Metadata)>0 {m:=make(map[string]string,len(r.Metadata));for k,v:=range r.Metadata {m[k]=v};r.Metadata=m} else {r.Metadata=nil}
	r.ReadOnlyReasons=nil
	return r
}

func domainTarget(value string,root bool) (string,bool) {
	if root && value=="." {return ".",true}
	if value!=strings.TrimSpace(value) {return "",false}
	v,err:=NormalizeZone(value);return v+".",err==nil
}

func uint16Value(n *int64) bool {return n!=nil && *n>=0 && *n<=65535}

func normalizeValue(r *Record) bool {
	if (r.Priority!=nil && r.Type!="MX" && r.Type!="SRV") || ((r.Weight!=nil || r.Port!=nil) && r.Type!="SRV") || ((r.CAAFlags!=nil || r.CAATag!="") && r.Type!="CAA") {return false}
	if utf8.RuneCountInString(r.Remark)>50 {return false}
	if r.LoadBalancingPolicy!="" && r.LoadBalancingPolicy!="round_robin" && r.LoadBalancingPolicy!="weight" {return false}
	if (r.LoadBalancingPolicy!="" || r.LoadBalancingWeight!=nil) && r.Type!="A" && r.Type!="AAAA" && r.Type!="CNAME" {return false}
	if r.LoadBalancingPolicy=="weight" && (r.LoadBalancingWeight==nil || *r.LoadBalancingWeight<1 || *r.LoadBalancingWeight>100) {return false}
	if r.LoadBalancingPolicy=="round_robin" && r.LoadBalancingWeight!=nil {return false}
	switch r.Type {
	case "A","AAAA":
		addr,err:=netip.ParseAddr(r.Value);if err!=nil || addr.Zone()!="" || (r.Type=="A" && !addr.Is4()) || (r.Type=="AAAA" && !addr.Is6()) {return false};r.Value=addr.String()
	case "TXT":
		return utf8.ValidString(r.Value) && len(r.Value)<=65535
	case "CNAME","NS","MX":
		value,ok:=domainTarget(r.Value,r.Type=="MX");if !ok{return false}
		if r.Type=="MX" && (!uint16Value(r.Priority) || (value=="." && *r.Priority!=0)) {return false};r.Value=value
	case "SRV":
		labels:=strings.Split(r.Name,".")
		if len(labels)<2 || len(labels[0])<2 || len(labels[1])<2 || labels[0][0]!='_' || labels[1][0]!='_' {return false}
		if r.Port==nil {
			parts:=strings.Fields(r.Value);if len(parts)!=4{return false}
			v:=make([]int64,3);for i:=range v {n,err:=strconv.ParseInt(parts[i],10,64);if err!=nil{return false};v[i]=n}
			r.Priority=&v[0];r.Weight=&v[1];r.Port=&v[2];r.Value=parts[3]
		}
		if !uint16Value(r.Priority) || !uint16Value(r.Weight) || !uint16Value(r.Port) {return false}
		value,ok:=domainTarget(r.Value,true);if !ok{return false};r.Value=value
	case "CAA":
		if r.CAAFlags==nil {
			if strings.ContainsAny(r.Value,"\r\n") {return false}
			rr,err:=dns.NewRR("owner.example. 600 IN CAA "+r.Value);if err!=nil{return false}
			caa,ok:=rr.(*dns.CAA);if !ok{return false};flags:=int64(caa.Flag);r.CAAFlags=&flags;r.CAATag=caa.Tag;r.Value=caa.Value
		}
		if *r.CAAFlags<0 || *r.CAAFlags>255 || r.CAATag=="" {return false}
		for _,c:=range r.CAATag {if !(c>='a' && c<='z' || c>='A' && c<='Z' || c>='0' && c<='9') {return false}}
	default:
		return false
	}
	return true
}

func jsonKey(v any) string {b,_:=json.Marshal(v);return string(b)}
func digest(v any) string {h:=sha256.Sum256([]byte(jsonKey(v)));return hex.EncodeToString(h[:])}

// BuildSnapshot assesses compatibility only. Even a compatible snapshot never
// grants write access or implies that the zone has completed adoption.
func BuildSnapshot(zone string, records []Record, limits Limits) (Snapshot,error) {
	zone,err:=NormalizeZone(zone);if err!=nil {return Snapshot{},err}
	s:=Snapshot{Zone:zone,Limits:limits,PolicyVersion:"alidns-v1",Records:make([]Record,0,len(records)),ReadOnlyReasons:[]string{},Compatible:true}
	zoneIssues:=map[string]bool{}
	if !limits.Known || limits.MinTTL<=0 || limits.MaxTTL<max(int64(600),limits.MinTTL) {zoneIssues["UNKNOWN_TTL_CAPABILITY"]=true}
	seen:=map[string]bool{};typesByName:=map[string]map[string]int{};ttlBySet:=map[string]int64{};nullMX:=map[string]bool{}
	for _,source:=range records {
		r:=copyRecord(source);r.Type=strings.ToUpper(r.Type)
		r.Name,err=normalizeOwner(zone,r.Name);if err!=nil{return Snapshot{},err}
		r.Protected=protected(r.Name,r.Type)
		issue:=func(code string){r.ReadOnlyReasons=append(r.ReadOnlyReasons,code);zoneIssues[code]=true}
		owner:=r.Name;if owner=="@" {owner=zone}else{owner+="."+zone}
		if !validName(owner,true) {issue("INVALID_RECORD_NAME")}
		if !r.Protected {
			if r.Line!="default" {issue("UNSUPPORTED_LINE")};if r.Status!="ENABLE" {issue("INACTIVE_RECORD")}
			if len(r.Metadata)>0 {issue("UNSUPPORTED_METADATA")}
			if r.TTL<max(int64(600),limits.MinTTL) || r.TTL>min(int64(86400),limits.MaxTTL) {issue("UNSUPPORTED_TTL")}
			normalized:=copyRecord(r)
			if !normalizeValue(&normalized) {issue("UNSUPPORTED_RECORD_VALUE")} else {
				r.Value=normalized.Value;r.Priority=normalized.Priority;r.Weight=normalized.Weight;r.Port=normalized.Port;r.CAAFlags=normalized.CAAFlags;r.CAATag=normalized.CAATag;r.Remark=normalized.Remark;r.LoadBalancingPolicy=normalized.LoadBalancingPolicy;r.LoadBalancingWeight=normalized.LoadBalancingWeight
			}
			if r.Name=="@" && r.Type=="CNAME" {issue("APEX_CNAME")}
			key:=r;key.ProviderRecordID="";key.ReadOnlyReasons=nil;key.LoadBalancingPolicy="";key.LoadBalancingWeight=nil
			if seen[jsonKey(key)] {issue("DUPLICATE_RECORD")};seen[jsonKey(key)]=true
			if typesByName[r.Name]==nil {typesByName[r.Name]=map[string]int{}};typesByName[r.Name][r.Type]++
			if r.Type=="MX" && r.Value=="." {nullMX[r.Name]=true}
			setKey:=r.Name+"/"+r.Type+"/"+r.Line
			if ttl,ok:=ttlBySet[setKey];ok && ttl!=r.TTL {issue("MIXED_RRSET_TTL")};ttlBySet[setKey]=r.TTL
		}
		s.Records=append(s.Records,r)
	}
	for name,types:=range typesByName {
		if types["CNAME"]>0 && (len(types)>1 || types["CNAME"]>1) {zoneIssues["CNAME_CONFLICT"]=true}
		if nullMX[name] && types["MX"]>1 {zoneIssues["NULL_MX_CONFLICT"]=true}
	}
	sort.Slice(s.Records,func(i,j int)bool{return jsonKey(s.Records[i])<jsonKey(s.Records[j])})
	semantic,identity:=[]Record{},[]Record{}
	for _,r:=range s.Records {if !r.Protected {r.ReadOnlyReasons=nil;identity=append(identity,r);r.ProviderRecordID="";semantic=append(semantic,r)}}
	sort.Slice(semantic,func(i,j int)bool{return jsonKey(semantic[i])<jsonKey(semantic[j])})
	sort.Slice(identity,func(i,j int)bool{return jsonKey(identity[i])<jsonKey(identity[j])})
	s.SnapshotHash=digest(struct{Zone,Policy string;Limits Limits;Records []Record}{zone,s.PolicyVersion,limits,semantic})
	s.BusinessIdentityHash=digest(struct{Semantic string;Records []Record}{s.SnapshotHash,identity})
	s.InventoryHash=digest(struct{Zone string;Records []Record}{zone,s.Records})
	for issue:=range zoneIssues {s.ReadOnlyReasons=append(s.ReadOnlyReasons,issue)};sort.Strings(s.ReadOnlyReasons);s.Compatible=len(s.ReadOnlyReasons)==0
	return s,nil
}
