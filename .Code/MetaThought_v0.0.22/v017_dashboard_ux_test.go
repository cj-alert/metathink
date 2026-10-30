package main

import (
	"regexp"
	"strings"
	"testing"
)

func v17UXSnapshot() DashboardSnapshot {
	s := v10ProteinSnapshot()
	s.Objective = "Develop a testable explanation."
	s.Goal = "# OBJECTIVE\nDevelop a **testable** explanation.\n\n## Evidence\nKeep <img src=x onerror=unsafe()> and <script>unsafe()</script> literal."
	s.Criteria = []ContractCriterion{{Name: "Novelty", Meaning: "Distinct explanation", Zero: "Known", One: "Novel", Target: &ScoreTarget{Operator: ">=", Value: 1}},
		{Name: "Testability", Meaning: "Useful test", Zero: "No test", One: "Clear test", Target: &ScoreTarget{Operator: ">", Value: .35}}}
	for i := range s.Teams {
		s.Teams[i].FriendlyName = []string{"First Mechanism Inquiry", "Second Mechanism Inquiry", "Third Mechanism Inquiry"}[i]
	}
	s.Teams[0].LatestWork = &DashboardWork{ID: 17, InquiryID: 1, DroneID: 2, Kind: "CONTRIBUTE", Phase: "contribute", Body: "Exact local work, not a parent assessment.", Relevance: .91, InquiryValue: .62}
	s.Queen.Note = "Original Queen explanation."
	s.Queen.ScoredDecisions = []DashboardQueenDecision{{Source: "resource_review", RecordID: 1, BirthsReviewed: 10, Body: "Original resource decision.", Relevance: .83, InquiryValue: .71}}
	return s
}

const v17UXDOMHelpers = `
function uxText(e){return [e.textContent||''].concat((e.children||[]).map(uxText)).join('\n');}
function uxNodes(e,predicate){return (predicate(e)?[e]:[]).concat(...(e.children||[]).map(function(child){return uxNodes(child,predicate);}));}
`

func TestV017DashboardVisualizationPopoutAndQueenView(t *testing.T) {
	s := v17UXSnapshot()
	s.Status = "paused"
	s.Queen.Note = "# Decision\nKeep the alternatives open.\n\n# Basis\nORIGINAL_QUEEN_BASIS"
	page, err := renderDashboard(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`<dialog\b[^>]*\bid="visualization-dialog"`).Match(page) {
		t.Fatal("visualization popout is not a native dialog")
	}
	controls := regexp.MustCompile(`(?s)<section\b[^>]*\bid="visualization-controls"[^>]*>.*?</section>`).FindString(string(page))
	if controls == "" || !strings.Contains(controls, `id="chromosome-legend"`) || strings.Contains(controls, `id="network"`) {
		t.Fatal("chromosome controls are not in their own section outside the visualization")
	}
	vm := v10DashboardScript(t, s, `{"view":"protein","selection":{"kind":"drone","id":1}}`)
	v10ProteinCheck(t, vm, v17UXDOMHelpers+`
const stage=document.getElementById('visualization-window'),home=document.getElementById('visualization-home'),dialog=document.getElementById('visualization-dialog'),host=document.getElementById('visualization-dialog-host');
const popout=document.getElementById('visualization-popout'),maximize=document.getElementById('visualization-maximize'),returnButton=document.getElementById('visualization-return');
const popoutFixtureCamera=JSON.stringify(proteinCamera),popoutFixtureSelection=JSON.stringify(selection);
popout.focus();popout.handlers.click();
if(!dialog.open||!dialog.modal||stage.parentNode!==host||home.children.includes(stage))throw new Error('popout did not move the existing stage into its modal');
if(document.getElementById('chromosome-legend').parentNode===stage)throw new Error('chromosome legend moved into the visualization stage');
maximize.handlers.click();
if(!dialog.open||stage.parentNode!==host||!dialog.classList.contains('maximized')||maximize.attributes['aria-pressed']!=='true')throw new Error('maximize did not expand the same modal stage');
if(JSON.stringify(proteinCamera)!==popoutFixtureCamera||JSON.stringify(selection)!==popoutFixtureSelection)throw new Error('popout/maximize changed camera or actor selection');
returnButton.handlers.click();
if(dialog.open||stage.parentNode!==home||home.children.filter(function(child){return child===stage;}).length!==1||host.children.includes(stage))throw new Error('return failed to restore exactly one original stage');
if(document.activeElement!==popout)throw new Error('return did not restore the initiating control focus');
popout.focus();popout.handlers.click();
let prevented=false;dialog.handlers.cancel({target:dialog,preventDefault:function(){prevented=true;}});
if(dialog.open||stage.parentNode!==home||document.activeElement!==popout)throw new Error('Escape/cancel failed to restore the dock and focus');
const queenButton=viewButtons.find(function(button){return button.dataset.view==='queen';});
if(!queenButton||typeof queenButton.handlers.click!=='function')throw new Error('Queen is not a third visualization view');
queenButton.handlers.click();
const queenView=document.getElementById('queen-view'),detail=document.getElementById('detail-panel');
if(view!=='queen'||queenView.hidden===true||document.getElementById('graph-view').hidden!==true||detail.parentNode!==queenView||detail.open!==true)throw new Error('Queen view did not visibly expose its full details');
if(!uxText(queenView).includes(snapshot.objective)||!uxText(queenView).includes('Keep the alternatives open.')||!uxText(queenView).includes('ORIGINAL_QUEEN_BASIS'))throw new Error('Queen view lost the actual objective, saved decision or basis');
tab='work';drawDetail();
if(!uxText(queenView).includes('Original resource decision.')||!uxText(queenView).includes('0.83')||!uxText(queenView).includes('0.71'))throw new Error('Queen view lost attributed original local scores');
if(!/paused/i.test(uxText(queenView)))throw new Error('Queen view conceals the paused execution state');
viewButtons.find(function(button){return button.dataset.view==='structure';}).handlers.click();
if(view!=='structure'||detail.parentNode!==document.getElementById('inspector-home')||document.getElementById('graph-view').hidden===true)throw new Error('Structure did not restore the normal inspector/graph');
`)
}

func TestV017DashboardClosedPanelsSafeMarkdownAndRefreshOptIn(t *testing.T) {
	s := v17UXSnapshot()
	page, err := renderDashboard(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, elementID := range []string{"governing-handoff", "parent-criteria-panel", "detail-panel", "uncommitted-drones"} {
		pattern := regexp.MustCompile(`<details\b[^>]*\bid="` + regexp.QuoteMeta(elementID) + `"[^>]*>`)
		opening := pattern.FindString(string(page))
		if opening == "" {
			t.Fatalf("%s is not a native collapsible details section", elementID)
		}
		if regexp.MustCompile(`\bopen(?:\s|=|>)`).MatchString(opening) {
			t.Fatalf("%s starts expanded: %s", elementID, opening)
		}
		if elementID == "parent-criteria-panel" && !strings.Contains(opening, `aria-label="Parent criterion scores and their evidence"`) {
			t.Fatal("collapsed parent criteria lost their accessible section label")
		}
	}
	parentSection := regexp.MustCompile(`(?s)<details\b[^>]*\bid="parent-criteria-panel"[^>]*>(.*?)</details>`).FindString(string(page))
	if !strings.Contains(parentSection, `<summary class="panel-head">`) || !strings.Contains(parentSection, "Parent criterion scores") || !strings.Contains(parentSection, `id="parent-score-grid"`) {
		t.Fatal("parent criteria heading and cards are not inside the same collapsible section")
	}
	compactCSS := strings.Join(strings.Fields(regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindString(string(page))), "")
	if !strings.Contains(compactCSS, `.parent-score-grid{grid-template-columns:repeat(2,minmax(0,1fr))}`) || !strings.Contains(compactCSS, `@media(max-width:700px){.parent-score-grid{grid-template-columns:minmax(0,1fr)}}`) {
		t.Fatal("parent inquiry cards lost their two-column desktop / one-column mobile layout intent")
	}
	if strings.Contains(string(page), "<script>unsafe()</script>") || strings.Contains(string(page), "<img src=x onerror=unsafe()>") {
		t.Fatal("saved request escaped the safe JSON/text boundary")
	}
	if got := v10DashboardEmbedded(t, page); got.Goal != s.Goal || got.Objective != s.Objective {
		t.Fatal("display formatting changed the saved governing text")
	}
	vm := v10DashboardScript(t, s, `{"autoRefresh":true}`)
	v10ProteinCheck(t, vm, v17UXDOMHelpers+`
if(autoRefresh!==false||Object.keys(recordedTimers).length!==0)throw new Error('legacy refresh preference silently enabled reload');
if(document.getElementById('goal').textContent!==snapshot.objective)throw new Error('closed governing panel does not show the actual short objective');
const markdown=new Element('div');renderMarkdown(markdown,snapshot.goal);
if(!uxNodes(markdown,function(e){return e.tagName==='h1';}).length||!uxNodes(markdown,function(e){return e.tagName==='h2';}).length||!uxNodes(markdown,function(e){return e.tagName==='strong';}).length)throw new Error('headings or bold text were not rendered');
if(uxNodes(markdown,function(e){return ['script','img','iframe'].includes(e.tagName)||typeof e.innerHTML!=='undefined';}).length)throw new Error('raw HTML entered the Markdown DOM');
if(!uxText(markdown).includes('<script>unsafe()</script>')||!uxText(markdown).includes('<img src=x onerror=unsafe()>'))throw new Error('unsafe-looking original text was deleted or rewritten');
if(['detail-panel','governing-handoff','parent-criteria-panel','uncommitted-drones'].some(function(id){return document.getElementById(id).open===true;}))throw new Error('a collapsed default was opened by initialization');
if(document.getElementById('parent-score-grid').children.filter(function(child){return child.className==='parent-score-team';}).length!==snapshot.teams.length)throw new Error('collapsing the parent section discarded its inquiry cards');
document.getElementById('refresh-toggle').handlers.click();
if(!autoRefresh||typeof recordedTimers[refreshTimer]!=='function')throw new Error('explicit refresh opt-in failed');
if(JSON.parse(savedPreferences).refreshPreferenceVersion!==2)throw new Error('new refresh preference was not versioned');
`)
	saved := v10ProteinCheck(t, vm, `savedPreferences`).String()
	reloaded := v10DashboardScript(t, s, saved)
	v10ProteinCheck(t, reloaded, `if(autoRefresh!==true||typeof recordedTimers[refreshTimer]!=='function')throw new Error('explicit opt-in did not survive reload');`)
}

func TestV017DashboardObjectiveTitleDoesNotChangeInstanceIdentity(t *testing.T) {
	s := v17UXSnapshot()
	s.InstanceName = "MetaThought_v0.0.17a - Saved run identity"
	s.RunFolder = "unchanged-saved-run-folder"
	s.RequestID = 17
	s.Objective = "Find novel testable explanations for a shared mechanism."
	s.DisplayTitle = dashboardDisplayTitle(s.InstanceName, s.Objective)
	prefix := "MetaThought_v0.0.17a - "
	if !strings.HasPrefix(s.DisplayTitle, prefix) {
		t.Fatalf("objective display lost version prefix: %q", s.DisplayTitle)
	}
	if count := len(strings.Fields(strings.TrimPrefix(s.DisplayTitle, prefix))); count < 3 || count > 5 {
		t.Fatalf("objective display has %d friendly words: %q", count, s.DisplayTitle)
	}
	if s.DisplayTitle == s.InstanceName {
		t.Fatal("title kept the folder label instead of the objective")
	}
	page, err := renderDashboard(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	decoded := v10DashboardEmbedded(t, page)
	if decoded.InstanceName != s.InstanceName || decoded.RunFolder != s.RunFolder || decoded.Objective != s.Objective || decoded.Goal != s.Goal {
		t.Fatal("display title changed saved instance, source folder, original objective or governing handoff")
	}
	if len(decoded.Teams) != len(s.Teams) {
		t.Fatal("display title changed saved inquiry collection")
	}
	for i := range s.Teams {
		if decoded.Teams[i].Question != s.Teams[i].Question {
			t.Fatal("display title changed an original inquiry question")
		}
	}
	vm := v10DashboardScript(t, s, `{}`)
	v10ProteinCheck(t, vm, `
if(document.getElementById('instance-name').textContent!==snapshot.display_title||!document.title.includes(snapshot.display_title))throw new Error('page heading or browser title does not show objective title');
const requestHeading=document.getElementById('request-title').textContent;
if(requestHeading.includes(snapshot.display_title)||requestHeading.includes(snapshot.instance_name)||!requestHeading.includes('17'))throw new Error('hero repeats the run title or lost request metadata');
if(Object.keys(elements).filter(function(key){return String(elements[key].textContent||'').includes(snapshot.display_title);}).length!==1)throw new Error('visible full run title is repeated');
if(document.getElementById('goal').textContent!==snapshot.objective)throw new Error('hero lost the actual objective');
if(snapshot.instance_name!=='MetaThought_v0.0.17a - Saved run identity'||snapshot.run_folder!=='unchanged-saved-run-folder')throw new Error('display title replaced source identity');
if(!storeKey.includes(snapshot.run_folder))throw new Error('preferences moved to a display-title identity');
`)
}

func TestV017DashboardPendingParentCriteriaAndQueenWorkStaySeparate(t *testing.T) {
	s := v17UXSnapshot()
	s.Teams[0].FriendlyName = "InternationalMechanisticConvergence NeurodevelopmentalHypotheses DiscriminatingExperiments"
	page, err := renderDashboard(s, 5)
	if err != nil {
		t.Fatal(err)
	}
	// The heading-specific rule overrides the older general card-button rule.
	// This checks the declared intent; the browser check verifies computed style.
	titleProperties := map[string]string{}
	for _, rule := range regexp.MustCompile(`\.parent-score-team\s+h3\s+\.textbutton\s*\{([^}]*)\}`).FindAllStringSubmatch(string(page), -1) {
		for _, declaration := range strings.Split(rule[1], ";") {
			if name, value, ok := strings.Cut(declaration, ":"); ok {
				titleProperties[strings.TrimSpace(name)] = strings.TrimSpace(value)
			}
		}
	}
	for name, want := range map[string]string{"text-align": "left", "white-space": "nowrap", "overflow": "hidden", "text-overflow": "ellipsis"} {
		if titleProperties[name] != want {
			t.Fatalf("parent inquiry title lost native one-line clipping rule %s:%s; got %q", name, want, titleProperties[name])
		}
	}
	vm := v10DashboardScript(t, s, `{"selection":{"kind":"team","id":1},"tab":"objective"}`)
	v10ProteinCheck(t, vm, v17UXDOMHelpers+`
const overview=document.getElementById('parent-score-grid');
const parentHeading=uxNodes(overview.children[0],function(e){return e.tagName==='button';})[0];
if(!parentHeading||parentHeading.textContent!==snapshot.teams[0].friendly_name)throw new Error('parent inquiry heading added an ID prefix or manually truncated its complete friendly name');
if(parentHeading.title!==snapshot.teams[0].question||typeof parentHeading.handlers.click!=='function')throw new Error('parent heading lost its original question or selection behavior');
if(!uxText(overview).includes('In Processing...'))throw new Error('missing parent assessments lack their processing state');
if(uxNodes(overview,function(e){return e.className==='criterion-value'&&/^\d/.test(e.textContent);}).length)throw new Error('local scores were promoted to parent grades');
if(!uxText(overview).includes('Novelty')||!uxText(overview).includes('Testability'))throw new Error('declared parent criteria are absent from interim view');
if(document.getElementById('detail-title').textContent!==snapshot.teams[0].friendly_name||Number(selection.id)!==1)throw new Error('friendly-only title or underlying inquiry identity was lost');
tab='work';drawDetail();
let detailText=uxText(document.getElementById('detail-content'));
if(!detailText.includes('0.91')||!detailText.includes('0.62')||!detailText.includes('not parent criteria'))throw new Error('local work scores or their meaning were lost');
select('queen',0);tab='objective';drawDetail();
detailText=uxText(document.getElementById('detail-content'));
if(detailText.includes(snapshot.goal)||detailText.includes('Exact governing handoff'))throw new Error('Queen detail duplicated the governing handoff');
tab='work';drawDetail();detailText=uxText(document.getElementById('detail-content'));
if(!detailText.includes('Original resource decision.')||!detailText.includes('0.83')||!detailText.includes('0.71'))throw new Error('saved Queen local scores or exact reason missing from Work');
if(/Work #1\b/.test(detailText))throw new Error('resource record was mislabeled as operational work');
if(!/resource/i.test(detailText))throw new Error('Queen score provenance was lost');
`)
}

func TestV017DashboardCollapsedQueenSummaryKeepsOnlyDecision(t *testing.T) {
	s := v17UXSnapshot()
	s.Status = "paused"
	s.Queen.Note = "# Decision\nContinue comparing the saved alternatives.\n\n# Basis\nBASIS_MUST_STAY_OUT_OF_PREVIEW\n\n# Need input\nINPUT_MUST_STAY_OUT_OF_PREVIEW"
	vm := v10DashboardScript(t, s, `{}`)
	v10ProteinCheck(t, vm, v17UXDOMHelpers+`
if(document.getElementById('detail-panel').open===true)throw new Error('Queen panel starts expanded');
if(document.getElementById('queen-summary').hidden===true)throw new Error('collapsed Queen summary is hidden');
const objective=uxText(document.getElementById('queen-summary-objective'));
const decision=uxText(document.getElementById('queen-summary-decision'));
if(!objective.includes(snapshot.objective))throw new Error('collapsed Queen summary lost the objective');
if(!decision.includes('Continue comparing the saved alternatives.')||decision.includes('BASIS_MUST_STAY_OUT_OF_PREVIEW')||decision.includes('INPUT_MUST_STAY_OUT_OF_PREVIEW'))throw new Error('Decision preview lost its exact section boundary');
if(!/paused/i.test(document.getElementById('status').textContent))throw new Error('processing workflow copy concealed paused execution');
select('team',2);
if(document.getElementById('queen-summary').hidden!==true)throw new Error('Queen-only preview remains on team inspection');
snapshot.queen.note='# Basis\nA decision is still needed.';select('queen',0);drawDetail();
const pendingDecision=uxText(document.getElementById('queen-summary-decision'));
if(!pendingDecision.includes('Decision: In Processing...')||pendingDecision.includes('A decision is still needed.'))throw new Error('missing Decision was replaced by Basis or a fabricated answer');
if(view!=='queen'||document.getElementById('detail-panel').open!==true)throw new Error('explicit Queen selection did not open its visualization details');
`)
}

func TestV017DashboardProteinRigidProjectionPreservesWorldLayoutAndHidesOnlyRoot(t *testing.T) {
	s := v17UXSnapshot()
	s.RootInquiryID = 1
	s.Teams = []DashboardTeam{
		{ID: 1, Question: "Original root request", FriendlyName: "Root Entry Inquiry"},
		{ID: 2, Question: "Second inquiry", FriendlyName: "Second Mechanism Inquiry", Members: []int64{1, 2}, ContributorCount: 2},
		{ID: 3, Question: "Third inquiry", FriendlyName: "Third Mechanism Inquiry", Members: []int64{1, 3}, ContributorCount: 2},
		{ID: 4, Question: "Fourth inquiry", FriendlyName: "Fourth Mechanism Inquiry", Members: []int64{3, 4}, ContributorCount: 2},
		{ID: 5, Question: "Fifth inquiry", FriendlyName: "Fifth Mechanism Inquiry", Members: []int64{4, 5, 6}, ContributorCount: 3},
	}
	s.Drones = []DashboardDrone{{ID: 1, Memberships: []int64{2, 3}}, {ID: 2, Memberships: []int64{2}},
		{ID: 3, Memberships: []int64{3, 4}}, {ID: 4, Memberships: []int64{4, 5}},
		{ID: 5, Memberships: []int64{5}}, {ID: 6, Memberships: []int64{5}}}
	s.Links = []DashboardLink{{WorkID: 31, FromID: 2, ToID: 3, DroneID: 1, ConnectionCount: 12},
		{WorkID: 32, FromID: 3, ToID: 4, DroneID: 3, ConnectionCount: 5},
		{WorkID: 33, FromID: 4, ToID: 5, DroneID: 4, ConnectionCount: 1},
		{WorkID: 34, FromID: 5, ToID: 2, DroneID: 5, ConnectionCount: 2},
		{WorkID: 35, FromID: 1, ToID: 2, DroneID: 1, ConnectionCount: 1}}
	s.Assessments = []map[string]any{{"inquiry_id": int64(1), "work_id": int64(99), "criterion": "Novelty", "score": .3, "evidence": "Original root assessment evidence"}}
	s.Queen.Note = "Decision: Continue inquiry #2; preserve root request #1."
	vm := v10DashboardScript(t, s, `{"view":"protein"}`)
	v10ProteinCheck(t, vm, v17UXDOMHelpers+`
const sourceBefore=JSON.stringify({teams:snapshot.teams,drones:snapshot.drones,links:snapshot.links,assessments:snapshot.assessments,root:snapshot.root_inquiry_id});
const sceneBefore=JSON.stringify(getProteinScene());
if(getProteinScene().groups.some(function(g){return g.id===snapshot.root_inquiry_id;})||getProteinScene().groups.length!==4)throw new Error('root inquiry remained in Protein or a real inquiry was removed');
if(getProteinScene().relations.some(function(r){return r.a===1||r.b===1;}))throw new Error('root relation survived Protein filtering');
const cameraCases=[[0,0,.55],[0,0,1],[.7,.35,1],[1.6,-.65,1],[Math.PI,.9,2],[-1.3,-1.1,10]];
cameraCases.forEach(function(camera){
 proteinCamera={yaw:camera[0],pitch:camera[1],zoom:camera[2]};drawGraph();
 const hulls=uxNodes(document.getElementById('network'),function(e){return e.attributes['data-protein-cluster']!==undefined;}).map(function(e){return {id:Number(e.attributes['data-protein-cluster']),x:Number(e.attributes.cx),y:Number(e.attributes.cy),r:Number(e.attributes.r)};});
 if(hulls.length!==4||hulls.some(function(h){return h.id===1||![h.x,h.y,h.r].every(Number.isFinite)||h.r<=0;}))throw new Error('projected hulls lost real inquiry identity or finite radius');
 hulls.forEach(function(hull){const group=getProteinScene().groups.find(function(g){return g.id===hull.id;}),projected=projectProtein(group);if(Math.abs(hull.x-projected.x)>.00001||Math.abs(hull.y-projected.y)>.00001||Math.abs(hull.r-group.r*projected.scale)>.00001)throw new Error('camera-dependent reflow moved a hull away from the rigid 3D projection: '+hull.id);});
});
if(JSON.stringify(getProteinScene())!==sceneBefore||JSON.stringify({teams:snapshot.teams,drones:snapshot.drones,links:snapshot.links,assessments:snapshot.assessments,root:snapshot.root_inquiry_id})!==sourceBefore)throw new Error('presentation changed the saved relationships, root evidence or underlying scene');
view='structure';drawGraph();
const structureText=uxText(document.getElementById('network'));
if(structureText.includes('Root Entry Inquiry')||structureText.includes('Original root request'))throw new Error('root entry point remains in Structure');
['Second Mechanism Inquiry','Third Mechanism Inquiry','Fourth Mechanism Inquiry','Fifth Mechanism Inquiry'].forEach(function(name){if(!structureText.includes(name))throw new Error('a real research inquiry disappeared from Structure');});
const parentCards=document.getElementById('parent-score-grid').children.filter(function(card){return card.className==='parent-score-team';});
if(parentCards.length!==4)throw new Error('root or root assessment still counted as a parent research card');
parentCards.forEach(function(card,index){const heading=uxNodes(card,function(e){return e.tagName==='button';})[0],original=snapshot.teams.find(function(team){return team.id===index+2;});if(!heading||heading.textContent!==original.friendly_name||heading.title!==original.question)throw new Error('research title is not the exact friendly name or original tooltip');heading.handlers.click();if(selection.kind!=='team'||Number(selection.id)!==original.id)throw new Error('friendly title selection changed the original research inquiry ID');});
if(document.getElementById('parent-score-count').textContent!=='0 inquiries with saved assessments')throw new Error('root-only grade inflated the visible assessment count');
const inquiryMetric=document.getElementById('metrics').children.find(function(card){return card.children.some(function(e){return e.tagName==='span'&&e.textContent==='Inquiries';});});
if(!inquiryMetric||Number(inquiryMetric.children.find(function(e){return e.tagName==='strong';}).textContent)!==4)throw new Error('displayed inquiry count still includes root');
if(!snapshot.teams.some(function(team){return team.id===1;}))throw new Error('root was deleted from source data');
setView('queen');
if(document.getElementById('queen-view').hidden===true||!uxText(document.getElementById('queen-view')).includes(snapshot.queen.note))throw new Error('hiding root removed Queen view or rewrote an original root reference');
`)
	stale := v10DashboardScript(t, s, `{"view":"protein","selection":{"kind":"team","id":1}}`)
	v10ProteinCheck(t, stale, `if(selection.kind!=='queen'||!snapshot.teams.some(function(t){return t.id===1;}))throw new Error('hidden-root saved selection was not recovered without deleting original data');`)
}

func TestV017DashboardStrongerConnectionsPullClustersCloser(t *testing.T) {
	distance := func(count int64) float64 {
		t.Helper()
		s := v17UXSnapshot()
		// The restored original layout weights actual saved offer rows.
		// No shared actors: only the number of those offers varies.
		s.Teams = s.Teams[:2]
		s.Teams[0].Members, s.Teams[1].Members = []int64{1, 2}, []int64{3, 4}
		for i := range s.Drones {
			if i < 2 {
				s.Drones[i].Memberships = []int64{1}
			} else {
				s.Drones[i].Memberships = []int64{2}
			}
		}
		s.Links = nil
		for i := int64(0); i < count; i++ {
			s.Links = append(s.Links, DashboardLink{WorkID: 30 + i, FromID: 1, ToID: 2, DroneID: 1, Body: "Exact saved offer", ConnectionCount: 1})
		}
		vm := v10DashboardScript(t, s, `{"view":"protein"}`)
		v10ProteinCheck(t, vm, v17UXDOMHelpers+`
const scene=getProteinScene(),groups=scene.groups;
if(groups.length!==2||scene.atoms.length!==4||scene.relations.length!==1)throw new Error('layout invented or discarded actors/relationships');
if(!groups.every(function(g){return [g.x,g.y,g.z,g.r].every(Number.isFinite);}))throw new Error('nonfinite cluster geometry');
const graph=document.getElementById('network');
if(!uxNodes(graph,function(e){return e.tagName==='radialGradient';}).length)throw new Error('sphere shading gradients missing');
function checkProteinLabelAlignment(group,labelText){
 const side=group.attributes['data-label-side'],box=group.children.find(function(e){return e.tagName==='rect';}),spans=labelText.children.filter(function(e){return e.tagName==='tspan';});
 if(!['top','bottom','left','right'].includes(side)||!box)throw new Error('label placement side or box missing');
 const left=Number(box.attributes.x),right=left+Number(box.attributes.width),center=(left+right)/2,expectedAnchor=side==='right'?'start':side==='left'?'end':'middle';
 if(labelText.attributes['text-anchor']!==expectedAnchor)throw new Error('incorrect text anchor for '+side+' label');
 const positions=spans.map(function(span){return Number(span.attributes.x);});
 if(positions.some(function(x){return !Number.isFinite(x)||Math.abs(x-positions[0])>.00001;}))throw new Error('label lines do not share an aligned x position');
 if((side==='right'&&!(positions[0]>left&&positions[0]<center))||(side==='left'&&!(positions[0]>center&&positions[0]<right))||((side==='top'||side==='bottom')&&Math.abs(positions[0]-center)>.00001))throw new Error('label text is not inset on its requested side or vertically centered');
}
teams.forEach(function(team){
 const groups=uxNodes(graph,function(e){return Number(e.attributes['data-inquiry-label'])===Number(team.id);});
 if(groups.length!==1)throw new Error('inquiry label attribution missing: '+team.id);
 const labels=uxNodes(groups[0],function(e){return e.tagName==='text'&&e.children.filter(function(child){return child.tagName==='tspan';}).length===3;});
 if(labels.length!==1)throw new Error('an inquiry lacks its three-line friendly name: '+team.id);
 const lines=labels[0].children.filter(function(child){return child.tagName==='tspan';}).map(function(span){return span.textContent.trim();});
 if(lines.some(function(line){return !line;}))throw new Error('a friendly-name line is empty: '+team.id);
 const combined=lines.join(' ').replace(/\s+/g,' ');
 if(combined!==team.friendly_name.trim().replace(/\s+/g,' '))throw new Error('three lines do not preserve the complete friendly name: '+combined);
 if(/Inquiry\s*#\s*\d/i.test(combined))throw new Error('inquiry ID remains in the visible friendly label');
 checkProteinLabelAlignment(groups[0],labels[0]);
});
// Exercise each placement state through the renderer without changing world
// geometry or requiring a particular collision-layout result in this fixture.
const originalLabelPlacement=proteinLabelPositions;
try{['top','bottom','right','left'].forEach(function(side){
 proteinLabelPositions=function(scene){const positions=originalLabelPlacement(scene);positions.forEach(function(position){position.side=side;});return positions;};drawGraph();
 uxNodes(graph,function(e){return e.attributes['data-inquiry-label']!==undefined;}).forEach(function(group){const text=group.children.find(function(e){return e.tagName==='text';});checkProteinLabelAlignment(group,text);});
});}finally{proteinLabelPositions=originalLabelPlacement;drawGraph();}
if(document.getElementById('graph-note').textContent!=='3D Protein View: recorded connections and shared members shape clusters. Shared atoms highlight together.')throw new Error('Protein footer differs from requested text');
`)
		return v10ProteinCheck(t, vm, `Math.hypot(groups[0].x-groups[1].x,groups[0].y-groups[1].y,groups[0].z-groups[1].z)`).ToFloat()
	}
	weak, strong := distance(1), distance(3)
	if !(strong > 0 && strong < weak) {
		t.Fatalf("three saved connections did not pull unchanged clusters closer: one=%v three=%v", weak, strong)
	}
}

func TestV017DashboardOriginalSpacingStaysStableAcrossSharedPatterns(t *testing.T) {
	for _, mode := range []string{"shared_triplet", "connect_only_triplet", "shared_pair"} {
		t.Run(mode, func(t *testing.T) {
			s := v17UXSnapshot()
			s.Links = nil
			s.RootInquiryID = 0
			switch mode {
			case "connect_only_triplet":
				s.Drones = nil
				for i := range s.Teams {
					a, b := int64(i*2+1), int64(i*2+2)
					s.Teams[i].Members = []int64{a, b}
					s.Drones = append(s.Drones, DashboardDrone{ID: a, Memberships: []int64{s.Teams[i].ID}}, DashboardDrone{ID: b, Memberships: []int64{s.Teams[i].ID}})
				}
				s.Links = []DashboardLink{{WorkID: 1, FromID: 1, ToID: 2, ConnectionCount: 50}, {WorkID: 2, FromID: 2, ToID: 3, ConnectionCount: 50}, {WorkID: 3, FromID: 1, ToID: 3, ConnectionCount: 50}}
			case "shared_pair":
				s.Teams = s.Teams[:2]
				s.Drones = []DashboardDrone{{ID: 1, Memberships: []int64{1, 2}}, {ID: 2, Memberships: []int64{1}}, {ID: 3, Memberships: []int64{2}}}
			}
			vm := v10DashboardScript(t, s, `{"view":"protein"}`)
			v10ProteinCheck(t, vm, v17UXDOMHelpers+`
const clusterGroups=getProteinScene().groups;
for(let i=0;i<clusterGroups.length;i++)for(let j=i+1;j<clusterGroups.length;j++){
 const a=clusterGroups[i],b=clusterGroups[j],gap=Math.hypot(a.x-b.x,a.y-b.y,a.z-b.z)-a.r-b.r;
 if(!Number.isFinite(gap)||gap<=.0001)throw new Error('original world spacing was replaced by overlapping or forced-contact hulls');
}
function v17WorldGeometry(){return getProteinScene().groups.map(function(g){return {id:g.id,x:g.x,y:g.y,z:g.z,r:g.r};});}
const initialWorld=JSON.stringify(v17WorldGeometry());
[[0,0,.55],[.7,.35,1],[Math.PI,-.9,2],[-1.3,1.1,10]].forEach(function(camera){proteinCamera={yaw:camera[0],pitch:camera[1],zoom:camera[2]};drawGraph();if(JSON.stringify(v17WorldGeometry())!==initialWorld)throw new Error('camera rotation or zoom rearranged the world clusters');});
teams.forEach(function(team,i){team.friendly_name='International Autonomic Integration Inquiry '+(i+1);});proteinSceneCache=null;drawGraph();
if(JSON.stringify(v17WorldGeometry())!==initialWorld)throw new Error('longer label text pushed the world hulls apart');
`)
		})
	}
}

func TestV017DashboardFriendlyNamesAndConnectionCountUseSavedState(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	question := "Does sleep-state arousal failure explain an observed response?"
	inquiry := v10Exec(t, h, "INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)", h.requestID, question, 2, now())
	var last int64
	for _, body := range []string{"First original offer", "Second original offer", "Latest original offer"} {
		last = v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,?,1,'expand','CONNECT',?,0.8,0.7,?)", inquiry, body, now())
	}
	v10Exec(t, h, "UPDATE hive_worker_queen SET note=? WHERE request_id=?", "Latest unscored NOOP note", h.requestID)
	guidance := v10Exec(t, h, "INSERT INTO hive_worker_guidance(request_id,inquiry_id,body,relevance,inquiry_value,created) VALUES(?,?,?,0.65,0.44,?)", h.requestID, inquiry, "Earlier scored guidance", now())
	v10Exec(t, h, "INSERT INTO hive_worker_resources(request_id,births_reviewed,selected_ids,note,relevance,inquiry_value,created) VALUES(?,10,'[]',?,0.9,0.75,?)", h.requestID, "Separately scored resource review", now())
	snapshot, err := h.dashboardSnapshot(v10Context(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Links) != 1 || snapshot.Links[0].ConnectionCount != 3 || snapshot.Links[0].WorkID != last || snapshot.Links[0].Body != "Latest original offer" {
		t.Fatalf("saved connection multiplicity or latest original lost: %#v", snapshot.Links)
	}
	for _, team := range snapshot.Teams {
		if team.ID != inquiry {
			continue
		}
		if team.Question != question {
			t.Fatal("friendly naming rewrote the original question")
		}
		if words := len(strings.Fields(team.FriendlyName)); words < 3 || words > 5 {
			t.Fatalf("friendly label has %d words: %q", words, team.FriendlyName)
		}
		if team.FriendlyName != dashboardFriendlyName(question) {
			t.Fatal("snapshot label bypassed the shared naming helper")
		}
	}
	if snapshot.Objective == "" || snapshot.Objective != dashboardObjective(v9Handoff) {
		t.Fatal("visible objective not derived from the saved governing request")
	}
	if snapshot.RootInquiryID != 1 {
		t.Fatalf("collector root identity=%d, want the lowest current-request inquiry", snapshot.RootInquiryID)
	}
	if snapshot.Queen.Note != "Latest unscored NOOP note" || len(snapshot.Queen.ScoredDecisions) != 2 {
		t.Fatalf("Queen score provenance or latest note lost: %#v", snapshot.Queen)
	}
	for _, decision := range snapshot.Queen.ScoredDecisions {
		switch decision.Source {
		case "guidance":
			if decision.RecordID != guidance || decision.Body != "Earlier scored guidance" || decision.Relevance != .65 || decision.InquiryValue != .44 {
				t.Fatalf("guidance scores moved to another note: %#v", decision)
			}
		case "resource_review":
			if decision.BirthsReviewed != 10 || decision.Body != "Separately scored resource review" || decision.Relevance != .9 || decision.InquiryValue != .75 {
				t.Fatalf("resource decision scores changed attribution: %#v", decision)
			}
		default:
			t.Fatalf("invented scored Queen source: %#v", decision)
		}
	}
	if h.Text.Calls != 0 {
		t.Fatal("dashboard made a model call")
	}
}
