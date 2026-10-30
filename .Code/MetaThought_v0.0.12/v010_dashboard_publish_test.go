package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestV010DashboardPublishesDistinctInstancesAndExactRefreshBytes(t *testing.T) {
	web := t.TempDir()
	first, _, _ := v10Fixture(t, 2, nil)
	second, _, _ := v10Fixture(t, 2, nil)
	first.Config.Dashboard = DashboardSettings{Enabled:true,RefreshSeconds:5,File:"{instance}.html",InstanceName:"MetaThought_v0.0.10h - Δ experiment",PublishDirectory:web}
	second.Config.Dashboard = DashboardSettings{Enabled:true,RefreshSeconds:5,File:"{instance}.html",InstanceName:"MetaThought_v0.0.10i - Another experiment",PublishDirectory:web}
	ctx := v10Context(t)
	claim := v10Claim(t, first, "preserved-dashboard-owner")
	before, err := first.query(ctx,"fixture_before_publish","hive_worker_queue","SELECT * FROM hive_worker_queue ORDER BY drone_id")
	if err != nil { t.Fatal(err) }
	var publishedPaths []string
	for _, h := range []*Hive{first,second} {
		snapshot, err := writeDashboard(ctx,h)
		if err != nil { t.Fatal(err) }
		instance,local,published,err := dashboardPaths(h.Config,h.Dir)
		if err != nil { t.Fatal(err) }
		if instance != h.Config.Dashboard.InstanceName || snapshot.InstanceName != instance || filepath.Base(local) != instance+".html" || filepath.Base(published) != instance+".html" { t.Fatalf("instance name or filename changed: instance=%q local=%q published=%q",instance,local,published) }
		localBytes,err:=os.ReadFile(local);if err!=nil{t.Fatal(err)}
		publishedBytes,err:=os.ReadFile(published);if err!=nil{t.Fatal(err)}
		if !bytes.Equal(localBytes,publishedBytes){t.Fatal("published bytes differ from the locally generated page")}
		publishedPaths=append(publishedPaths,published)
	}
	if publishedPaths[0]==publishedPaths[1]{t.Fatal("different instances collide in the publication directory")}
	after,err:=first.query(ctx,"fixture_after_publish","hive_worker_queue","SELECT * FROM hive_worker_queue ORDER BY drone_id");if err!=nil{t.Fatal(err)}
	if !reflect.DeepEqual(before,after){t.Fatal("publishing changed worker ownership or phase")}
	if v9Count(t,first.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='working' AND result IS NULL",first.requestID)!=1{t.Fatal("publishing changed the inquiry status")}
	otherBefore,err:=os.ReadFile(publishedPaths[1]);if err!=nil{t.Fatal(err)}
	v10Exec(t,first,"UPDATE hive_requests SET status='returned',result=?,finish_reason=? WHERE id=?","Updated exact conclusion.","convergence_returned",first.requestID)
	if _,err=writeDashboard(ctx,first);err!=nil{t.Fatal(err)}
	_,local,published,err:=dashboardPaths(first.Config,first.Dir);if err!=nil{t.Fatal(err)}
	localBytes,err:=os.ReadFile(local);if err!=nil{t.Fatal(err)}
	updated,err:=os.ReadFile(published);if err!=nil{t.Fatal(err)}
	if !bytes.Equal(localBytes,updated){t.Fatal("refresh published different bytes")}
	final:=v10DashboardEmbedded(t,updated)
	if final.Status!="returned"||final.Result!="Updated exact conclusion."{t.Fatalf("published refresh is stale: %+v",final)}
	otherAfter,err:=os.ReadFile(publishedPaths[1]);if err!=nil{t.Fatal(err)}
	if !bytes.Equal(otherBefore,otherAfter){t.Fatal("refresh overwrote another instance")}
	if first.Text.Calls!=0||second.Text.Calls!=0{t.Fatal("dashboard publication invoked a provider")}
	t.Logf("distinct instance pages updated with exact local bytes; owner %s preserved; no model calls",claim.Owner)
}

func TestV010DashboardPublicationNamesAndDirectoriesAreExplicit(t *testing.T) {
	config:=v10Config(t,2,10)
	web:=t.TempDir()
	config.Dashboard=DashboardSettings{Enabled:true,RefreshSeconds:5,File:"{instance}.html",PublishDirectory:web}
	run:=filepath.Join(t.TempDir(),"MetaThought_v0.0.10h - Unicode Δ")
	instance,local,published,err:=dashboardPaths(config,run)
	if err!=nil{t.Fatal(err)}
	if instance!=filepath.Base(run)||filepath.Base(local)!=instance+".html"||filepath.Base(published)!=instance+".html"{t.Fatal("default identity did not preserve the exact run-folder basename")}
	for name,change:=range map[string]func(*DashboardSettings){
		"no instance filename":func(d *DashboardSettings){d.File="dashboard.html"},
		"path in instance":func(d *DashboardSettings){d.InstanceName="../different"},
		"unknown placeholder":func(d *DashboardSettings){d.File="{instance}-{unknown}.html"},
	}{t.Run(name,func(t *testing.T){candidate:=config;change(&candidate.Dashboard);if _,_,_,err:=dashboardPaths(candidate,run);err==nil{t.Fatal("invalid publication destination was accepted")}})}
	if _,err=os.Stat(filepath.Join(web,"missing"));!os.IsNotExist(err){t.Fatalf("validation created a publication directory: %v",err)}
}
