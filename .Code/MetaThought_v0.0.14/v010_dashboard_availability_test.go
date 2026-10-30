package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestV010DashboardUnavailablePublicationDoesNotBlockHive(t *testing.T) {
	h, _, dir := v10Fixture(t, 2, nil)
	h.Config.Dashboard = DashboardSettings{Enabled:true,RefreshSeconds:3600,File:"{instance}.html",InstanceName:"unavailable-webroot",PublishDirectory:filepath.Join(t.TempDir(),"missing-webroot")}
	ctx := v10Context(t)
	if err := h.Config.Validate(); err != nil { t.Fatalf("machine-local hosting availability invalidated hive configuration: %v",err) }
	claim:=v10Claim(t,h,"still-thinking")
	before,err:=h.query(ctx,"fixture_before_display","hive_worker_queue","SELECT * FROM hive_worker_queue ORDER BY drone_id")
	if err!=nil{t.Fatal(err)}
	// Direct generation (and therefore the CLI) reports publication failure,
	// while retaining the complete local page generated before that failure.
	if _,err=writeDashboard(ctx,h);err==nil{t.Fatal("direct publication failure was hidden")}
	local:=filepath.Join(dir,"unavailable-webroot.html")
	if body,readErr:=os.ReadFile(local);readErr!=nil||len(body)==0{t.Fatalf("local page was lost with publication failure: %v",readErr)}
	stop,err:=startDashboard(ctx,h)
	if err!=nil||stop==nil{t.Fatalf("optional hosting prevented the hive watcher from starting: %v",err)}
	stop()
	after,err:=h.query(ctx,"fixture_after_display","hive_worker_queue","SELECT * FROM hive_worker_queue ORDER BY drone_id")
	if err!=nil{t.Fatal(err)}
	if !reflect.DeepEqual(before,after)||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='working'",h.requestID)!=1{t.Fatal("dashboard hosting failure changed operational state")}
	if v9Count(t,h.Logger.db,"SELECT COUNT(*) FROM hive_log WHERE event='dashboard_publish_failed'")==0||v9Count(t,h.Logger.db,"SELECT COUNT(*) FROM hive_log WHERE event='dashboard_update_failed'")==0{t.Fatal("hosting failure was not recorded in developer diagnostics")}
	if h.Text.Calls!=0{t.Fatal("dashboard failure invoked a model")}
	if _,err=os.Stat(h.Config.Dashboard.PublishDirectory);!os.IsNotExist(err){t.Fatalf("dashboard unexpectedly created the absent webroot: %v",err)}
	t.Logf("unavailable webroot remained an explicit publication error; local page saved and owner %s retained",claim.Owner)
}
