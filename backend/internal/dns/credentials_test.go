package dns

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const fixtureConfig=`{"access_key_id":"fixture-key-id","access_key_secret":"fixture-secret"}`

func TestCredentialConfigValidatesAndRedacts(t *testing.T) {
	c,err:=parseCredential(fixtureConfig);if err!=nil {t.Fatal(err)}
	if c.accessKeyID!="fixture-key-id" || c.accessKeySecret!="fixture-secret" {t.Fatal("wrong credential mapping")}
	b,_:=json.Marshal(c)
	for _,v:=range []string{string(b),fmt.Sprintf("%v %+v %#v %s %q",c,c,c,c,c),fmt.Sprintf("%+v",&c)} {if strings.Contains(v,"fixture-") {t.Fatal("credential leaked through serialization/formatting")}}
	for _,raw:=range []string{`{}`,`null`,`{"access_key_id":"id"}`,`{"access_key_id":"id","access_key_secret":" "}`,fixtureConfig+`{}`,`{"access_key_id":"a","access_key_id":"b","access_key_secret":"secret"}`,strings.Repeat("x",65537)} {
		_,err:=parseCredential(raw);if !errors.Is(err,ErrCredential) {t.Fatalf("unsafe credential accepted: %v",err)}
	}
}

func credentialDB(t *testing.T)*sql.DB {
	t.Helper();db,err:=sql.Open("sqlite",":memory:");if err!=nil{t.Fatal(err)};db.SetMaxOpenConns(1);t.Cleanup(func(){db.Close()})
	_,err=db.Exec(`CREATE TABLE access(id INTEGER PRIMARY KEY,name TEXT NOT NULL,type TEXT NOT NULL,config TEXT NOT NULL)`);if err!=nil{t.Fatal(err)}
	for _,row:=range []struct{id int;name,typ,config string}{{1,"Aliyun fixture","aliyun",fixtureConfig},{2,"Other","cloudflare",fixtureConfig},{3,"Broken","aliyun","not json"}} {
		if _,err:=db.Exec(`INSERT INTO access(id,name,type,config) VALUES(?,?,?,?)`,row.id,row.name,row.typ,row.config);err!=nil{t.Fatal(err)}
	};return db
}

func TestCredentialStoreSummariesNeverReturnConfig(t *testing.T) {
	s:=SQLCredentialStore{DB:credentialDB(t)}
	rows,err:=s.List(context.Background());if err!=nil || len(rows)!=2 {t.Fatalf("wrong credential list: count=%d err=%v",len(rows),err)}
	b,_:=json.Marshal(rows);var decoded []map[string]any;json.Unmarshal(b,&decoded)
	for _,row:=range decoded {if len(row)!=3 || row["type"]!="aliyun" || row["id"]==nil || row["name"]==nil {t.Fatalf("overexposed summary: %s",b)}}
	if strings.Contains(string(b),"fixture-secret") || strings.Contains(string(b),"config") {t.Fatal("list leaked config")}
	c,err:=s.Resolve(context.Background(),1);if err!=nil || c.accessKeySecret!="fixture-secret" {t.Fatalf("cannot resolve scoped credential: %v",err)}
	for _,id:=range []int64{0,-1,2,3,999} {if _,err:=s.Resolve(context.Background(),id);!errors.Is(err,ErrCredential){t.Fatalf("accepted wrong/malformed credential id=%d",id)}}
	s.DB.Close();if rows,err:=s.List(context.Background());err==nil || rows!=nil {t.Fatal("closed database returned partial success")}
}

func TestCredentialListDoesNotReadConfigColumn(t *testing.T) {
	db,err:=sql.Open("sqlite",":memory:");if err!=nil{t.Fatal(err)};defer db.Close();db.SetMaxOpenConns(1)
	if _,err=db.Exec(`CREATE TABLE access(id INTEGER,name TEXT,type TEXT); INSERT INTO access VALUES(1,'Summary only','aliyun')`);err!=nil{t.Fatal(err)}
	rows,err:=(SQLCredentialStore{DB:db}).List(context.Background());if err!=nil || len(rows)!=1 {t.Fatalf("summary unnecessarily selected secrets: %v",err)}
}
