package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Real saved assessment rows feed the production collector and rendered script.
// The DOM stand-in checks text/state only; this is not visual browser proof.
func TestV010DashboardReasonsIncompleteAndLegacyEvidence(t *testing.T) {
	h, _, _ := v10Fixture(t, 2, nil)
	ctx := v10Context(t)
	criteria, err := workerHandoffCriteria(v9Handoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(criteria) != 3 {
		t.Fatal("fixture requires three declared criteria")
	}
	var legacy, current strings.Builder
	reason := "A prior-art search is needed before novelty can be assessed. </script><script>neverRun()</script>"
	evidence := "No prior-art search or external verification is present in this fixture."
	uncertainty := "The candidate's novelty remains unknown."
	for index, criterion := range criteria {
		fmt.Fprintf(&legacy, "# %s\nScore: 0\nEvidence: LEGACY_EVIDENCE_ZERO_BASIS\nUncertainty: Original legacy uncertainty.\n", criterion.Name)
		switch index {
		case 0:
			fmt.Fprintf(&current, "# %s\nScore: INCOMPLETE\nReason: %s\nEvidence: %s\nUncertainty: %s\n", criterion.Name, reason, evidence, uncertainty)
		case 1:
			fmt.Fprintf(&current, "# %s\nScore: 0\nReason: The fixture candidate does not meet this criterion.\nEvidence: NUMERIC_ZERO_IS_A_JUDGMENT\nUncertainty: This applies only to the synthetic fixture.\n", criterion.Name)
		default:
			fmt.Fprintf(&current, "# %s\nScore: 0.35000000000000003\nReason: Preserve the actual reported value.\nEvidence: STRICT_THRESHOLD_VALUE\nUncertainty: No calibrated probability is claimed.\n", criterion.Name)
		}
	}
	for _, builder := range []*strings.Builder{&legacy, &current} {
		builder.WriteString("# YOUR SCORE\nrelevance: 0.8\ninquiry_value: 0.7\n")
	}
	insert := func(body string) int64 {
		t.Helper()
		return v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)", body, now())
	}
	legacyID, currentID := insert(legacy.String()), insert(current.String())
	researchBody := "Exact RESEARCH answer.\nOriginal search evidence: fixture-only source text & attribution."
	v10Exec(t, h, "INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'contribute','RESEARCH',?,0.8,0.7,?)", researchBody, now())
	snapshot, err := h.dashboardSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Assessments) != 6 {
		t.Fatalf("expected two complete saved profile shapes, got %#v", snapshot.Assessments)
	}
	for _, row := range snapshot.Assessments {
		if row["parse_error"] != nil {
			t.Fatalf("production dashboard could not parse the saved profile: %#v", row)
		}
		if id(row["work_id"]) == legacyID {
			if row["score"] != float64(0) || row["incomplete"] != false || row["reason"] != "" || row["evidence"] != "LEGACY_EVIDENCE_ZERO_BASIS" {
				t.Fatalf("legacy zero/evidence was reinterpreted or given an invented reason: %#v", row)
			}
			continue
		}
		if id(row["work_id"]) != currentID || id(row["drone_id"]) != 2 || id(row["inquiry_id"]) != 1 {
			t.Fatalf("assessment attribution changed: %#v", row)
		}
		switch row["criterion"] {
		case criteria[0].Name:
			if row["score"] != nil || row["incomplete"] != true || row["reason"] != reason || row["evidence"] != evidence || row["uncertainty"] != uncertainty {
				t.Fatalf("incomplete assessment became a grade or lost its original basis: %#v", row)
			}
		case criteria[1].Name:
			if row["score"] != float64(0) || row["incomplete"] != false {
				t.Fatalf("an actual numeric zero was converted into missing evidence: %#v", row)
			}
		}
	}
	page, err := renderDashboard(snapshot, 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "<script>neverRun()</script>") {
		t.Fatal("original Reason escaped its plain-text/JSON boundary")
	}
	decoded := v10DashboardEmbedded(t, page)
	if len(decoded.Assessments) != 6 || decoded.Assessments[3]["score"] != nil || decoded.Assessments[3]["reason"] != reason {
		t.Fatalf("embedded JSON lost null score or exact reason: %#v", decoded.Assessments)
	}
	if snapshot.Teams[0].LatestWork == nil || snapshot.Teams[0].LatestWork.Kind != "RESEARCH" || snapshot.Teams[0].LatestWork.Body != researchBody {
		t.Fatal("latest original research body/evidence was rewritten or hidden")
	}
	vm := v10DashboardScript(t, snapshot, `{"selection":{"kind":"team","id":1},"tab":"objective"}`)
	if err := vm.Set("expectedReason", reason); err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("expectedEvidence", evidence); err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("expectedResearch", researchBody); err != nil {
		t.Fatal(err)
	}
	_, err = vm.RunString(`
function reasonText(e){return [e.textContent||""].concat(e.children.map(reasonText)).join("\n");}
function reasonNodes(e,cls){return (e.className===cls?[e]:[]).concat(...e.children.map(function(child){return reasonNodes(child,cls);}));}
const detail=document.getElementById("detail-content");
const directCards=detail.children.filter(function(e){return e.className==="criterion-score";});
if(directCards.length!==3)throw new Error("Current display mixed profile vintages or omitted criteria");
if(reasonNodes(directCards[0],"criterion-value")[0].textContent!=="In Processing...")throw new Error("Incomplete became a numeric or failed grade");
if(reasonNodes(directCards[1],"criterion-value")[0].textContent!=="0")throw new Error("A real zero was treated as missing");
if(reasonNodes(directCards[2],"criterion-value")[0].textContent!=="0.35000000000000003")throw new Error("Strict-threshold score was rounded");
if(reasonNodes(directCards[2],"criterion-target")[0].textContent!=="Target: > 0.35")throw new Error("Strict target operator changed");
const firstText=reasonText(directCards[0]);
if(firstText.indexOf(expectedReason)<0||firstText.indexOf(expectedEvidence)<0||firstText.indexOf(expectedReason)>firstText.indexOf("Technical evidence"))throw new Error("Original plain-language reason is missing or below technical evidence");
if(!Object.prototype.hasOwnProperty.call(directCards[0].attributes,"open"))throw new Error("Default team reason requires hunting another tab");
const history=detail.children.filter(function(e){return e.className==="score-history";})[0];
if(!history||!reasonText(history).includes("Plain-language reason not recorded")||!reasonText(history).includes("LEGACY_EVIDENCE_ZERO_BASIS"))throw new Error("Legacy reason was fabricated or older evidence discarded");
if(!reasonText(document.getElementById("parent-score-grid")).includes(expectedReason))throw new Error("Overall score overview lost original reason");
tab="work";drawDetail();
if(!reasonText(document.getElementById("detail-content")).includes(expectedResearch)||!reasonText(document.getElementById("detail-content")).includes("Local work scores · not parent criteria"))throw new Error("Research body or local/parent score separation lost");
`)
	if err != nil {
		t.Fatal(err)
	}
	if h.Text.Calls != 0 {
		t.Fatalf("dashboard caused %d model calls", h.Text.Calls)
	}
	t.Logf("saved profiles #%d/#%d retain legacy zero and evidence; incomplete is null/ungraded with exact Reason above Evidence; history and original RESEARCH text remain visible", legacyID, currentID)
}

func TestV010DashboardUsesPageScrollingOnly(t *testing.T) {
	page, err := renderDashboard(DashboardSnapshot{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	_, remainder, ok := strings.Cut(string(page), "<style>")
	if !ok {
		t.Fatal("dashboard stylesheet missing")
	}
	style, _, ok := strings.Cut(remainder, "</style>")
	if !ok {
		t.Fatal("dashboard stylesheet end missing")
	}
	windowed := regexp.MustCompile(`(?i)overflow(?:-x|-y)?\s*:\s*(?:auto|scroll)|max-height\s*:\s*[0-9]|position\s*:\s*sticky|-webkit-line-clamp`)
	modalScope := regexp.MustCompile(`^#visualization-dialog(?:$|[\s.:\[])`)
	modalBounds := ""
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(style, -1) {
		selector, declarations := strings.TrimSpace(rule[1]), rule[2]
		if selector == "#visualization-dialog" {
			modalBounds = declarations
		}
		if match := windowed.FindString(declarations); match != "" {
			modalOnly := true
			for _, part := range strings.Split(selector, ",") {
				if !modalScope.MatchString(strings.TrimSpace(part)) {
					modalOnly = false
				}
			}
			if !modalOnly || strings.Contains(strings.ToLower(declarations), "sticky") || strings.Contains(strings.ToLower(declarations), "-webkit-line-clamp") {
				t.Fatalf("ordinary dashboard page reintroduced a scrolling/capped subsection: selector=%q property=%q", selector, match)
			}
		}
	}
	for _, bound := range []string{"max-width:100vw", "max-height:100dvh", "width:min(", "height:min("} {
		if !strings.Contains(strings.Join(strings.Fields(modalBounds), ""), bound) {
			t.Fatalf("explicit visualization modal lost viewport bound %q", bound)
		}
	}
	t.Log("ordinary page sections retain page scrolling without height caps, sticky panes or line clamps; the explicit visualization modal is viewport bounded")
}
