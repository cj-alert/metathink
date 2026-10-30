package main

import (
 "context"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func v21ResearchFixture(t *testing.T, h *Hive) (int64, int64, string, string) {
 t.Helper()
 completed := jsonText(map[string]any{"question":"Earlier trial question", "status":"completed", "answer":"Original earlier trial finding", "search_calls":[]any{map[string]any{"id":"original-completed-search", "status":"completed"}}, "citations":[]any{map[string]any{"url":"https://example.test/original-trial"}}})
 incomplete := jsonText(map[string]any{"question":"Later public disclosure question", "status":"incomplete", "answer":"Useful partial findings; page retrieval failed", "search_calls":[]any{map[string]any{"id":"original-failed-open", "status":"failed"}}})
 a := v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'expand','RESEARCH',?,0.8,0.7,?)",completed,now())
 b := v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'expand','RESEARCH',?,0.8,0.7,?)",incomplete,now())
 v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'contribute','CONTRIBUTE','Later synthesis must not hide research',0.8,0.7,?)",now())
 other := v10Exec(t,h,"INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,'Unrelated inquiry',0,?)",h.requestID,now())
 v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,1,'expand','RESEARCH','UNRELATED_RESEARCH_CANARY',0.8,0.7,?)",other,now())
 return a,b,completed,incomplete
}

func v21CheckHistory(t *testing.T, request TextRequest, first, last int64, completed, incomplete string) map[string]any {
 t.Helper()
 text := str(request.Messages[1]["content"])
 if strings.Contains(text,v9DiagnosticCanary) || strings.Contains(text,"UNRELATED_RESEARCH_CANARY") { t.Fatal("diagnostics or unrelated research entered formal review") }
 var input map[string]any
 if err := json.Unmarshal([]byte(text),&input); err != nil { t.Fatal(err) }
 history,ok := input["research_history"].([]any)
 if !ok || len(history)!=2 { t.Fatalf("earlier same-author research lost: %v",input["research_history"]) }
 for index,want := range []struct{id int64;body string}{{first,completed},{last,incomplete}} {
  row:=history[index].(map[string]any)
  if id(row["id"])!=want.id || str(row["body"])!=want.body { t.Fatal("original research order, status or evidence changed") }
 }
 latest:=input["research_work"].(map[string]any)
 if id(latest["id"])!=last || str(latest["body"])!=incomplete {t.Fatal("latest research compatibility/status changed")}
 if str(input["handoff"])!=v9Handoff {t.Fatal("review changed governing contract")}
 if !strings.Contains(str(request.Messages[0]["content"]),"not a required list or count") {t.Fatal("review did not receive optional-category instruction")}
 return input
}

func TestV021FormalReportAndAssessmentKeepEarlierResearch(t *testing.T) {
 h,_,_:=v10Fixture(t,2,nil)
 v10ConvergenceTeam(t,h,2)
 first,last,completed,incomplete:=v21ResearchFixture(t,h)
 counts:=map[string]int{}
 h.Text.Inner=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
  input:=v21CheckHistory(t,request,first,last,completed,incomplete)
  purpose:=str(input["purpose"]);counts[purpose]++
  switch purpose {
  case "team_report":return TextResponse{Text:v9Reply("CONTRIBUTE 1","Earlier trial retained; later retrieval incomplete.",0.8,0.7)},nil
  case "team_assessment":return TextResponse{Text:v10ConvergenceAssessment("0.8")},nil
  }
  return TextResponse{},errors.New("only report and assessment are authorized")
 })
 criteria,err:=workerHandoffCriteria(v9Handoff);if err!=nil{t.Fatal(err)}
 report,err:=h.completeWorkerTeam(v10Context(t),v9Handoff,criteria,InquiryTeam{InquiryID:1,Members:[]int64{1,2},Contributors:[]int64{1,2}})
 if err!=nil || report.AssessmentID==0 || counts["team_report"]!=1 || counts["team_assessment"]!=1 {t.Fatalf("report=%+v counts=%v error=%v",report,counts,err)}
 t.Log("Both production prompt paths received earlier same-author research and original incomplete status, without unrelated research or diagnostics")
}

func TestV021IsolatedSavedReviewLeavesSourceHiveUntouched(t *testing.T) {
 h,c,baseline:=v10Fixture(t,2,nil)
 v10ConvergenceTeam(t,h,2)
 first,last,completed,incomplete:=v21ResearchFixture(t,h)
 body:=v9Reply("CONTRIBUTE 1","Saved original report; useful partial findings are not completed retrieval.",0.8,0.7)
 reportID:=v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'convergence','TEAM_REPORT',?,0.8,0.7,?)",body,now())
 originalAssessment:=v10ConvergenceAssessment("0.2")
 v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)",originalAssessment,now())
 before,err:=h.query(v10Context(t),"before_review","hive_inquiry_work","SELECT * FROM hive_inquiry_work ORDER BY id");if err!=nil{t.Fatal(err)}
 out:=filepath.Join(t.TempDir(),"review")
 calls:=0
 reply:=v10ConvergenceAssessment("0.8")
 result,err:=ReviewSavedTeam(v10Context(t),c,baseline,out,reportID,v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
  calls++
  if request.Scope!="drone" || request.Model!=c.Models.Drone {t.Fatal("review used a replacement Queen grader")}
  input:=v21CheckHistory(t,request,first,last,completed,incomplete)
  report:=input["report"].(map[string]any)
  if str(report["body"])!=body || id(report["id"])!=reportID || id(input["drone_id"])!=2 {t.Fatal("original report or assessor was replaced")}
  return TextResponse{Text:reply},nil
 }))
 if err!=nil || calls!=1 || str(result["outcome"])!="assessed" || str(result["source_state_mode"])!="ro" {t.Fatalf("result=%v calls=%d error=%v",result,calls,err)}
 after,err:=h.query(v10Context(t),"after_review","hive_inquiry_work","SELECT * FROM hive_inquiry_work ORDER BY id");if err!=nil||jsonText(before)!=jsonText(after){t.Fatal("isolated review changed source work or grades")}
 if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE status='converging'")!=1 {t.Fatal("isolated review changed source lifecycle")}
 saved,err:=os.ReadFile(filepath.Join(out,"review-reply.txt"));if err!=nil||string(saved)!=reply{t.Fatal("new raw reply was not preserved verbatim")}
 t.Log("One isolated drone assessment, original report/grades unchanged, source state read-only, no hive exploration")
}
