package main

import (
	"testing"

	"github.com/dop251/goja"
)

func v10ProteinSnapshot() DashboardSnapshot {
	return DashboardSnapshot{InstanceName:"3D fixture",RunFolder:"protein-fixture",Status:"working",WorkerLimit:3,TeamCount:3,
		Teams:[]DashboardTeam{
			{ID:1,Question:"First inquiry",Members:[]int64{1,2},ContributorCount:2},
			{ID:2,Question:"Second inquiry",Members:[]int64{1,3},ContributorCount:2},
			{ID:3,Question:"Third inquiry",Members:[]int64{3,4},ContributorCount:2},
		},
		Drones:[]DashboardDrone{
			{ID:1,Name:"Shared one",Memberships:[]int64{1,2}},
			{ID:2,Name:"Two",Memberships:[]int64{1}},
			{ID:3,Name:"Shared three",Memberships:[]int64{2,3}},
			{ID:4,Name:"Four",Memberships:[]int64{3}},
		},
		Links:[]DashboardLink{{WorkID:10,FromID:1,ToID:3,DroneID:1,Body:"An original connection offer"}},
	}
}

func v10ProteinCheck(t *testing.T,vm *goja.Runtime,code string) goja.Value {
	t.Helper()
	value,err:=vm.RunString(code)
	if err!=nil{t.Fatalf("rendered 3D dashboard script: %v",err)}
	return value
}

func TestV010ProteinPerspectiveAndDepthPreserveRelationships(t *testing.T) {
	vm:=v10DashboardScript(t,v10ProteinSnapshot(),`{"view":"protein"}`)
	v10ProteinCheck(t,vm,`
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

func TestV010ProteinDragClickCameraPersistenceAndRefresh(t *testing.T) {
	snapshot:=v10ProteinSnapshot()
	vm:=v10DashboardScript(t,snapshot,`{"view":"protein"}`)
	v10ProteinCheck(t,vm,`
 const defaultCamera=JSON.stringify(proteinCamera),root=document.getElementById('network');
 if(root.handlers.pointerdown!==proteinPointerDown||root.handlers.pointermove!==proteinPointerMove||root.handlers.pointerup!==proteinPointerUp||root.handlers.click!==proteinClick)throw new Error('3D input handlers were not attached to the stable SVG root');
 const queuedRefresh=recordedTimers[refreshTimer];if(typeof queuedRefresh!=='function')throw new Error('refresh was not scheduled');
 const picked=new Element('g');picked.__proteinPick={kind:'drone',id:1};
 const child=new Element('circle');picked.append(child);root.append(picked);
 function pointer(x,y){return {button:0,pointerId:1,clientX:x,clientY:y,target:child,preventDefault:function(){},stopImmediatePropagation:function(){}};}
 const originalSelection=JSON.stringify(selection);
 proteinPointerDown(pointer(100,100));
 if(!proteinGesture)throw new Error('drag did not start');
 queuedRefresh();
 if(reloadCount!==0)throw new Error('refresh interrupted a gesture');
 proteinPointerMove(pointer(155,125));proteinPointerUp(pointer(155,125));
 if(proteinGesture!==null)throw new Error('drag did not end');
 proteinClick(pointer(155,125));
 if(JSON.stringify(selection)!==originalSelection)throw new Error('drag selected the crossed object');
 if(JSON.stringify(proteinCamera)===defaultCamera)throw new Error('drag did not rotate camera');
 proteinPointerDown(pointer(180,140));proteinPointerUp(pointer(180,140));proteinClick(pointer(180,140));
 if(selection.kind!=='drone'||Number(selection.id)!==1)throw new Error('ordinary click failed to select the original drone');
 const oldZoom=proteinCamera.zoom;proteinZoom(1.2);
 if(!(proteinCamera.zoom>oldZoom))throw new Error('zoom failed');
 const stored=JSON.parse(savedPreferences);
 if(stored.view!=='protein'||JSON.stringify(stored.proteinCamera)!==JSON.stringify(proteinCamera))throw new Error('camera/view not persisted');
 `)
	saved:=v10ProteinCheck(t,vm,`savedPreferences`).String()
	wantCamera:=v10ProteinCheck(t,vm,`JSON.stringify(proteinCamera)`).String()
	reloaded:=v10DashboardScript(t,snapshot,saved)
	if got:=v10ProteinCheck(t,reloaded,`JSON.stringify(proteinCamera)`).String();got!=wantCamera{t.Fatalf("reload lost camera: got %s want %s",got,wantCamera)}
	v10ProteinCheck(t,vm,`
 proteinReset();if(JSON.stringify(proteinCamera)!==defaultCamera)throw new Error('reset did not restore default camera');
 proteinPointerDown(pointer(220,170));proteinPointerMove(pointer(245,190));proteinPointerCancel(pointer(245,190));
 if(proteinGesture!==null)throw new Error('cancel left a live gesture');
 view='structure';save();drawGraph();
 proteinPointerDown(pointer(10,10));if(proteinGesture!==null)throw new Error('Structure view started a protein gesture');
 if(JSON.parse(savedPreferences).view!=='structure')throw new Error('view switch did not persist');
 const freshRefresh=recordedTimers[refreshTimer];if(typeof freshRefresh!=='function')throw new Error('gesture did not schedule a fresh refresh interval');
 freshRefresh();if(reloadCount!==1)throw new Error('refresh did not resume after gesture completion');
 `)
	t.Log("drag rotated without selecting; click selected; camera/view survived reload; reset/cancel/Structure switch and refresh deferral completed")
}

func TestV010ProteinEmptyAndSingleInquiryRemainFinite(t *testing.T) {
	for name,snapshot:=range map[string]DashboardSnapshot{
		"empty":{RunFolder:"empty-protein",Status:"not_started"},
		"single inquiry":{RunFolder:"single-protein",Status:"working",Teams:[]DashboardTeam{{ID:1,Question:"One inquiry",Members:[]int64{1}}},Drones:[]DashboardDrone{{ID:1,Name:"One",Memberships:[]int64{1}}}},
	}{t.Run(name,func(t *testing.T){
		vm:=v10DashboardScript(t,snapshot,`{"view":"protein"}`)
		v10ProteinCheck(t,vm,`
 const p=projectProtein({x:0,y:0,z:0});
 if(![p.x,p.y,p.z,p.scale].every(Number.isFinite))throw new Error('invalid degenerate projection');
 if(!proteinDrawItems().every(function(item){return Number.isFinite(item.depth);}))throw new Error('invalid degenerate item depth');
 proteinZoom(1.1);proteinReset();drawGraph();
 if(/NaN|Infinity/.test(JSON.stringify(document.getElementById('network').children,function(k,v){return k==='parentNode'?undefined:v;})))throw new Error('invalid SVG geometry');
 `)
	})}
}
