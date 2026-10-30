package main

import (
	"testing"

	"github.com/dop251/goja"
)

func v10ProteinSnapshot() DashboardSnapshot {
	return DashboardSnapshot{InstanceName: "3D fixture", RunFolder: "protein-fixture", Status: "working", WorkerLimit: 3, TeamCount: 3,
		Teams: []DashboardTeam{
			{ID: 1, Question: "First inquiry", Members: []int64{1, 2}, ContributorCount: 2},
			{ID: 2, Question: "Second inquiry", Members: []int64{1, 3}, ContributorCount: 2},
			{ID: 3, Question: "Third inquiry", Members: []int64{3, 4}, ContributorCount: 2},
		},
		Drones: []DashboardDrone{
			{ID: 1, Name: "Shared one", Memberships: []int64{1, 2}},
			{ID: 2, Name: "Two", Memberships: []int64{1}},
			{ID: 3, Name: "Shared three", Memberships: []int64{2, 3}},
			{ID: 4, Name: "Four", Memberships: []int64{3}},
		},
		Links: []DashboardLink{{WorkID: 10, FromID: 1, ToID: 3, DroneID: 1, Body: "An original connection offer"}},
	}
}

func v10ProteinCheck(t *testing.T, vm *goja.Runtime, code string) goja.Value {
	t.Helper()
	value, err := vm.RunString(code)
	if err != nil {
		t.Fatalf("rendered 3D dashboard script: %v", err)
	}
	return value
}

func TestV010ProteinPerspectiveAndDepthPreserveRelationships(t *testing.T) {
	vm := v10DashboardScript(t, v10ProteinSnapshot(), `{"view":"protein"}`)
	v10ProteinCheck(t, vm, `
 const originalRelationships=JSON.stringify({teams:teams,drones:drones,links:links});
 const originalScene=JSON.stringify(getProteinScene());
 const beforeProjection=projectProtein({x:80,y:35,z:100});
 const beforeOrder=proteinDrawItems().filter(function(x){return x.kind==='atom';}).map(function(x){return x.id+':'+x.teamID;}).join('|');
 proteinCamera.yaw+=Math.PI;proteinCamera.pitch=-proteinCamera.pitch;drawGraph();
 const afterProjection=projectProtein({x:80,y:35,z:100});
 const afterItems=proteinDrawItems();
 const afterOrder=afterItems.filter(function(x){return x.kind==='atom';}).map(function(x){return x.id+':'+x.teamID;}).join('|');
 if(beforeProjection.x===afterProjection.x&&beforeProjection.y===afterProjection.y)throw new Error('rotation did not change projection');
 if(beforeOrder===afterOrder)throw new Error('rotation did not change atom depth order');
 if(!afterItems.every(function(x,i){return Number.isFinite(x.depth)&&(i===0||afterItems[i-1].depth<=x.depth);}))throw new Error('items are not painted far-to-near');
 if(JSON.stringify(getProteinScene())!==originalScene||JSON.stringify({teams:teams,drones:drones,links:links})!==originalRelationships)throw new Error('camera changed actor IDs, memberships or scene relationships');
 proteinCamera.yaw=0;proteinCamera.pitch=0;
 const nearPoint=projectProtein({x:30,y:20,z:80}),farPoint=projectProtein({x:30,y:20,z:-80});
 if(![nearPoint.x,nearPoint.y,nearPoint.z,nearPoint.scale,farPoint.x,farPoint.y,farPoint.z,farPoint.scale].every(Number.isFinite)||nearPoint.scale===farPoint.scale)throw new Error('perspective does not depend on depth');
 `)
	t.Log("rotation changed projection and far-to-near ordering while actor IDs, memberships and saved links remained identical")
}

func TestV010ProteinButtonsClickCameraPersistenceAndRefresh(t *testing.T) {
	snapshot := v10ProteinSnapshot()
	vm := v10DashboardScript(t, snapshot, `{"view":"protein"}`)
	v10ProteinCheck(t, vm, `
 const defaultCamera=JSON.stringify(proteinCamera),root=document.getElementById('network');
 if(['pointerdown','pointermove','pointerup','pointercancel'].some(function(name){return typeof root.handlers[name]!=='function';}))throw new Error('mouse/touch rotation handlers are missing');
 if(autoRefresh!==false||Object.keys(recordedTimers).length!==0)throw new Error('refresh must start disabled');
 const originalSelection=JSON.stringify(selection);
 ['left','right','up','down'].forEach(function(direction){if(typeof document.getElementById('protein-rotate-'+direction).handlers.click!=='function')throw new Error('rotation button missing: '+direction);});
 document.getElementById('protein-rotate-right').handlers.click();
 document.getElementById('protein-rotate-up').handlers.click();
 if(JSON.stringify(selection)!==originalSelection)throw new Error('rotation button changed selected actor');
 if(JSON.stringify(proteinCamera)===defaultCamera)throw new Error('buttons did not rotate camera');
 const picked=root.children.find(function(element){return Number(element.attributes['data-drone-id'])===1;});
 if(!picked)throw new Error('ordinary atom selection target was lost');
 dashboardTargetClick(root,picked,'mouse');
 if(selection.kind!=='drone'||Number(selection.id)!==1)throw new Error('ordinary click failed to select the original drone');
 const oldZoom=proteinCamera.zoom;proteinZoom(1.2);
 if(!(proteinCamera.zoom>oldZoom))throw new Error('zoom failed');
 proteinZoom(1000);
 if(proteinCamera.zoom!==10||document.getElementById('protein-zoom-label').textContent!=='1000%')throw new Error('zoom does not reach its 1000% limit');
 const stored=JSON.parse(savedPreferences);
 if(stored.view!=='protein'||JSON.stringify(stored.proteinCamera)!==JSON.stringify(proteinCamera)||stored.refreshPreferenceVersion!==2||stored.autoRefresh!==false)throw new Error('camera/view or explicit refresh state not persisted');
 `)
	saved := v10ProteinCheck(t, vm, `savedPreferences`).String()
	wantCamera := v10ProteinCheck(t, vm, `JSON.stringify(proteinCamera)`).String()
	reloaded := v10DashboardScript(t, snapshot, saved)
	if got := v10ProteinCheck(t, reloaded, `JSON.stringify(proteinCamera)`).String(); got != wantCamera {
		t.Fatalf("reload lost camera: got %s want %s", got, wantCamera)
	}
	v10ProteinCheck(t, vm, `
 proteinReset();if(JSON.stringify(proteinCamera)!==defaultCamera)throw new Error('reset did not restore default camera');
 view='structure';save();drawGraph();
 if(JSON.parse(savedPreferences).view!=='structure')throw new Error('view switch did not persist');
 document.getElementById('refresh-toggle').handlers.click();
 if(!autoRefresh)throw new Error('explicit refresh opt-in failed');
 const freshRefresh=recordedTimers[refreshTimer];if(typeof freshRefresh!=='function')throw new Error('explicit refresh opt-in did not schedule a callback');
 freshRefresh();if(reloadCount!==1)throw new Error('opted-in refresh did not reload');
 document.getElementById('refresh-toggle').handlers.click();
 if(autoRefresh||Object.keys(recordedTimers).length!==0)throw new Error('refresh opt-out did not clear its callback');
 `)
	t.Log("buttons rotated without changing selection; click selected; camera/view and disabled refresh survived reload; zoom reached 1000%; reset, Structure switch and explicit refresh opt-in/out completed")
}

func TestV010ProteinEmptyAndSingleInquiryRemainFinite(t *testing.T) {
	for name, snapshot := range map[string]DashboardSnapshot{
		"empty":          {RunFolder: "empty-protein", Status: "not_started"},
		"single inquiry": {RunFolder: "single-protein", Status: "working", Teams: []DashboardTeam{{ID: 1, Question: "One inquiry", Members: []int64{1}}}, Drones: []DashboardDrone{{ID: 1, Name: "One", Memberships: []int64{1}}}},
	} {
		t.Run(name, func(t *testing.T) {
			vm := v10DashboardScript(t, snapshot, `{"view":"protein"}`)
			v10ProteinCheck(t, vm, `
 const p=projectProtein({x:0,y:0,z:0});
 if(![p.x,p.y,p.z,p.scale].every(Number.isFinite))throw new Error('invalid degenerate projection');
 if(!proteinDrawItems().every(function(item){return Number.isFinite(item.depth);}))throw new Error('invalid degenerate item depth');
 proteinZoom(1.1);proteinReset();drawGraph();
 if(/NaN|Infinity/.test(JSON.stringify(document.getElementById('network').children,function(k,v){return k==='parentNode'?undefined:v;})))throw new Error('invalid SVG geometry');
 `)
		})
	}
}

func TestV010ProteinMouseTouchAndModalDeferRefresh(t *testing.T) {
	vm := v10DashboardScript(t, v10ProteinSnapshot(), `{"view":"protein"}`)
	v10ProteinCheck(t, vm, `
const root=document.getElementById('network');
function pointerEvent(id,type,x,y,target){return {pointerId:id,pointerType:type,isPrimary:true,button:0,buttons:1,clientX:x,clientY:y,target:target,preventDefault:function(){},stopPropagation:function(){}};}
function pickTarget(id){const atom=root.children.find(function(element){return Number(element.attributes['data-drone-id'])===(id||1);});if(!atom)throw new Error('requested atom is absent');const child=new Element('tspan');atom.append(child);return {atom:atom,child:child};}
function clickTarget(pick,type){dashboardTargetClick(root,pick.child,type);}
['mouse','touch'].forEach(function(type,index){
 select('queen',0);view='protein';drawGraph();
 const pick=pickTarget(),id=index+11,beforeCamera=JSON.stringify(proteinCamera),beforeSelection=JSON.stringify(selection);
 root.handlers.pointerdown(pointerEvent(id,type,120,130,pick.child));
 if(!root.hasPointerCapture(id))throw new Error(type+' gesture did not capture the stable SVG root');
 root.handlers.pointermove(pointerEvent(id,type,124,130,pick.child));
 if(JSON.stringify(proteinCamera)!==beforeCamera)throw new Error(type+' rotation began at the original 4px threshold instead of beyond it');
 root.handlers.pointermove(pointerEvent(id+100,type,300,300,pick.child));
 if(JSON.stringify(proteinCamera)!==beforeCamera)throw new Error(type+' unrelated pointer moved the active gesture');
 root.handlers.pointermove(pointerEvent(id,type,190,165,pick.child));
 if(JSON.stringify(proteinCamera)===beforeCamera)throw new Error(type+' drag did not rotate in 3D');
 const originalCamera=JSON.parse(beforeCamera);
 if(Math.abs(proteinCamera.yaw-(originalCamera.yaw+70*.007))>1e-12||Math.abs(proteinCamera.pitch-Math.max(-1.45,Math.min(1.45,originalCamera.pitch+35*.007)))>1e-12)throw new Error(type+' drag changed original sensitivity or pitch bounds');
 if(JSON.stringify(selection)!==beforeSelection)throw new Error(type+' drag changed selected actor');
 root.handlers.pointerup(pointerEvent(id,type,190,165,pick.child));
 if(root.hasPointerCapture(id))throw new Error(type+' release kept pointer capture');
 clickTarget(pick,type);
 if(JSON.stringify(selection)!==beforeSelection)throw new Error(type+' post-drag click incorrectly selected an actor');
 const deliberate=pickTarget();
 root.handlers.pointerdown(pointerEvent(id+20,type,210,180,deliberate.child));root.handlers.pointerup(pointerEvent(id+20,type,210,180,deliberate.child));clickTarget(deliberate,type);
 if(selection.kind!=='drone'||Number(selection.id)!==1)throw new Error(type+' deliberate click after drag did not select the original actor');
});
document.getElementById('refresh-toggle').handlers.click();
const refreshBeforeDrag=recordedTimers[refreshTimer];if(typeof refreshBeforeDrag!=='function')throw new Error('opt-in refresh timer absent before gesture');
const pick=pickTarget(2),selectionBeforeCancel=JSON.stringify(selection);root.handlers.pointerdown(pointerEvent(70,'touch',100,100,pick.child));
if(Object.keys(recordedTimers).length!==0)throw new Error('active gesture left a refresh timer scheduled');refreshBeforeDrag();
if(reloadCount!==0)throw new Error('refresh interrupted an active gesture');
root.handlers.pointercancel(pointerEvent(70,'touch',100,100,pick.child));
if(root.hasPointerCapture(70))throw new Error('cancel did not release pointer capture');
const cancelledCamera=JSON.stringify(proteinCamera);root.handlers.pointermove(pointerEvent(70,'touch',250,220,pick.child));
if(JSON.stringify(proteinCamera)!==cancelledCamera)throw new Error('cancelled gesture still rotates');
clickTarget(pick,'touch');if(JSON.stringify(selection)!==selectionBeforeCancel)throw new Error('original cancellation no longer suppresses the following synthetic click');
const refreshBeforeModal=recordedTimers[refreshTimer];if(typeof refreshBeforeModal!=='function')throw new Error('cancel failed to restore refresh schedule');
document.getElementById('visualization-popout').handlers.click();
if(Object.keys(recordedTimers).length!==0)throw new Error('open modal left a refresh timer scheduled');refreshBeforeModal();
if(reloadCount!==0)throw new Error('refresh interrupted an open visualization dialog');
document.getElementById('visualization-return').handlers.click();
const refreshAfterReturn=recordedTimers[refreshTimer];if(typeof refreshAfterReturn!=='function')throw new Error('return failed to restore refresh schedule');refreshAfterReturn();
if(reloadCount!==1)throw new Error('opt-in refresh did not resume after gesture/modal completion');
`)
}
