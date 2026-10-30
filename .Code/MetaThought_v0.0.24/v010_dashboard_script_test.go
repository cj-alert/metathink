package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// This is a deterministic script/state check, not a browser or visual renderer.
// The small DOM stand-in records element structure and text only. It has no
// network, layout, file, automatic timer execution, or browser-security bypass.
// Tests may explicitly invoke recorded callbacks for deterministic interactions.
const v10DashboardDOM = `
function Element(tag){this.tagName=tag;this.children=[];this.style={};this.dataset={};this.attributes={};this.handlers={};this.textContent="";var classes={};this.classList={toggle:function(name,force){var add=force===undefined?!classes[name]:!!force;if(add)classes[name]=true;else delete classes[name];return add;},contains:function(name){return !!classes[name];},add:function(name){classes[name]=true;},remove:function(name){delete classes[name];}};}
Element.prototype.append=function(){for(var i=0;i<arguments.length;i++){var child=arguments[i];if(child.parentNode){var siblings=child.parentNode.children,index=siblings.indexOf(child);if(index>=0)siblings.splice(index,1);}this.children.push(child);child.parentNode=this;}};
Element.prototype.replaceChildren=function(){this.children.forEach(function(child){child.parentNode=null;});this.children=[];this.append.apply(this,arguments);};
Element.prototype.setAttribute=function(name,value){this.attributes[name]=value;};
Element.prototype.addEventListener=function(name,handler){this.handlers[name]=handler;};
Element.prototype.getBoundingClientRect=function(){return {left:0,top:0,width:1000,height:640};};
Element.prototype.setPointerCapture=function(id){this.capturedPointer=id;};
Element.prototype.hasPointerCapture=function(id){return this.capturedPointer===id;};
Element.prototype.releasePointerCapture=function(id){if(this.capturedPointer===id)this.capturedPointer=null;};
Element.prototype.focus=function(){document.activeElement=this;};
Element.prototype.showModal=function(){this.open=true;this.modal=true;};
Element.prototype.close=function(){this.open=false;this.modal=false;if(this.handlers.close)this.handlers.close({target:this});};
const elements={};
const tabButtons=["objective","work","reports"].map(function(name){var e=new Element("button");e.dataset.tab=name;return e;});
const viewButtons=["structure","protein","queen"].map(function(name){var e=new Element("button");e.dataset.view=name;return e;});
const document={getElementById:function(id){if(!elements[id])elements[id]=new Element("div");return elements[id];},createElement:function(tag){return new Element(tag);},createElementNS:function(ns,tag){return new Element(tag);},querySelectorAll:function(selector){return selector==="[data-tab]"?tabButtons:selector==="[data-view]"?viewButtons:[];}};
// Only the static docks needed for native-dialog reparenting are modeled.
// The browser check, not this stand-in, verifies their actual page layout.
document.getElementById('visualization-home').append(document.getElementById('visualization-window'));
document.getElementById('visualization-controls').append(document.getElementById('chromosome-legend'));
document.getElementById('visualization-window').append(document.getElementById('protein-controls'),document.getElementById('graph-view'),document.getElementById('queen-view'));
document.getElementById('graph-view').append(document.getElementById('network'),document.getElementById('graph-note'));
document.getElementById('visualization-dialog').append(document.getElementById('visualization-dialog-host'));
document.getElementById('inspector-home').append(document.getElementById('detail-panel'));
document.getElementById('detail-panel').append(document.getElementById('detail-title'),document.getElementById('detail-meta'),document.getElementById('queen-summary'),document.getElementById('detail-content'));
document.getElementById('queen-summary').append(document.getElementById('queen-summary-objective'),document.getElementById('queen-summary-decision'));
// Dispatch the SVG's capturing click listener before its target/bubble handler.
function dashboardTargetClick(root,target,type){var stopped=false,event={target:target,pointerType:type||'mouse',detail:1,preventDefault:function(){},stopPropagation:function(){stopped=true;},stopImmediatePropagation:function(){stopped=true;}};if(root.handlers.click)root.handlers.click(event);for(var current=target;current&&current!==root&&!stopped;current=current.parentNode){if(current.handlers.click)current.handlers.click(event);}}
const localStorage={getItem:function(){return savedPreferences;},setItem:function(key,value){savedPreferences=value;}};
let reloadCount=0;const location={reload:function(){reloadCount++;}};
let timerSequence=0;const recordedTimers={};
function setTimeout(callback){const id=++timerSequence;recordedTimers[id]=callback;return id;}
function clearTimeout(id){delete recordedTimers[id];}
`

func v10DashboardScript(t *testing.T, snapshot DashboardSnapshot, preferences string) *goja.Runtime {
	t.Helper()
	html, err := renderDashboard(snapshot, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(string(html), "<script>\n")
	if !ok {
		t.Fatal("dashboard script start missing")
	}
	script, _, ok = strings.Cut(script, "</script>")
	if !ok {
		t.Fatal("dashboard script end missing")
	}
	program, err := goja.Compile("dashboard.js", script, false)
	if err != nil {
		t.Fatalf("dashboard JavaScript syntax: %v", err)
	}
	vm := goja.New()
	if err := vm.Set("savedPreferences", preferences); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(v10DashboardDOM); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunProgram(program); err != nil {
		t.Fatalf("dashboard initial deterministic state: %v", err)
	}
	return vm
}

func TestV010DashboardScriptState(t *testing.T) {
	t.Run("population_is_not_worker_slots", func(t *testing.T) {
		s := DashboardSnapshot{RunFolder: "fixture", RequestID: 1, Status: "working", WorkerLimit: 10,
			Queen: DashboardQueen{Owner: "queen-owner", FocusID: 2}, TeamCount: 2,
			Teams: []DashboardTeam{{ID: 1, Question: "First inquiry", FriendlyName: "First Mechanism Inquiry", Members: []int64{1, 2}, ContributorCount: 2}, {ID: 2, Question: "Second inquiry", FriendlyName: "Second Mechanism Inquiry", Members: []int64{1, 3}, ContributorCount: 2}},
			Links: []DashboardLink{{WorkID: 7, FromID: 1, ToID: 2, DroneID: 1, Body: "Original offered connection"}},
		}
		for n := int64(1); n <= 138; n++ {
			d := DashboardDrone{ID: n, Name: fmt.Sprintf("drone-%d", n)}
			switch n {
			case 1:
				d.Memberships = []int64{1, 2}
			case 2:
				d.Memberships = []int64{1}
			case 3:
				d.Memberships = []int64{2}
			}
			s.Drones = append(s.Drones, d)
			w := DashboardWorker{DroneID: n, Phase: "contribute", FocusID: 1, State: "ready"}
			if n <= 9 {
				w.Owner, w.State = fmt.Sprintf("worker-%d", n), "active"
			}
			s.Workers = append(s.Workers, w)
		}
		// Null preferences must be harmless. The first source list deliberately
		// contains all 138 queue rows, of which only nine have actor claims.
		vm := v10DashboardScript(t, s, "null")
		check := func(code string) goja.Value {
			t.Helper()
			value, err := vm.RunString(code)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		if count := check(`document.getElementById("workers").children.length`).ToInteger(); count != 10 {
			t.Fatalf("138 drone queue records became %d worker slots", count)
		}
		if got := check(`document.getElementById("worker-total").textContent`).String(); got != "10 claimed / 10 simultaneous calls" {
			t.Fatalf("Queen omitted or double-counted in worker allocation: %s", got)
		}
		if count := check(`document.getElementById("workers").children.filter(function(card){return card.children.some(function(e){return e.className==="worker-name"&&e.textContent==="Queen";});}).length`).ToInteger(); count != 1 {
			t.Fatalf("expected one Queen slot, got %d", count)
		}
		if count := check(`document.getElementById("pool").children.length`).ToInteger(); count != 135 {
			t.Fatalf("uncommitted population not fully represented: %d", count)
		}
		check(`view="protein";drawGraph();select("drone",138);tab="reports";drawDetail();select("link",7);view="structure";drawGraph();`)
		// A saved numeric-string ID still resolves after reload; a Queen claim
		// already represented by the collector is not added a second time.
		s.Workers = append(s.Workers, DashboardWorker{Owner: "queen-owner", Phase: "queen_monitor", State: "active"})
		vm = v10DashboardScript(t, s, `{"selection":{"kind":"team","id":"2"},"view":"protein","tab":"work"}`)
		if got := check(`document.getElementById("detail-title").textContent`).String(); got != "Second Mechanism Inquiry" || check(`Number(selection.id)`).ToInteger() != 2 {
			t.Fatalf("saved selection was lost or mis-typed: %s", got)
		}
		if got := check(`document.getElementById("worker-total").textContent`).String(); got != "10 claimed / 10 simultaneous calls" {
			t.Fatalf("Queen claim counted twice: %s", got)
		}
	})
	t.Run("null_arrays_and_stale_selection", func(t *testing.T) {
		s := DashboardSnapshot{WorkerLimit: 10, Status: "not_started"}
		vm := v10DashboardScript(t, s, `{"selection":{"kind":"team","id":999},"view":"protein"}`)
		value, err := vm.RunString(`selection.kind+":"+document.getElementById("workers").children.length`)
		if err != nil || value.String() != "queen:10" {
			t.Fatalf("empty snapshot or stale selection failed: value=%v error=%v", value, err)
		}
	})
}
