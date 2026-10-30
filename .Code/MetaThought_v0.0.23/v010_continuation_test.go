package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func v10ContinueReply(reportID int64) string {
	return fmt.Sprintf("# ACTION\nCONTINUE\n# REPORTS\n%d\n# CRITERIA\nUNMET\n# YOUR ANSWER\nThe low profile is not completion. Investigate a different explanation using FIRST_LOW_SCORE_EVIDENCE; retain the earlier hypothesis and uncertainty.\n# YOUR SCORE\nrelevance: 0.9\ninquiry_value: 0.8\n",reportID)
}

func v10ContinuationProfile(score,evidence string) string {
	return strings.ReplaceAll(v10ConvergenceAssessment(score),"Fixture contains no external research or experiment.",evidence)
}

func TestV010ContinuationExploresFromScoresAndReassessesBeforeReturn(t *testing.T) {
	h,config,dir:=v10Fixture(t,2,nil)
	config.Dashboard.Enabled=false
	v10ConvergenceTeam(t,h,2)
	var mu sync.Mutex
	counts:=map[string]int{}
	var firstReport,finalReport int64
	client:=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
		text:=str(request.Messages[1]["content"])
		var input map[string]any
		if err:=json.Unmarshal([]byte(text),&input);err!=nil{return TextResponse{},err}
		if strings.Contains(text,v9DiagnosticCanary)||strings.Contains(text,v9OldSeedCanary){return TextResponse{},errors.New("diagnostics or preset contract leaked into continuation")}
		purpose:=str(input["purpose"])
		if purpose==""{if request.Model==config.Models.Queen{purpose="monitor"}else{purpose="exploration"}}
		mu.Lock();counts[purpose]++;number:=counts[purpose];mu.Unlock()
		switch purpose {
		case "team_report":
			if number>1&&!strings.Contains(text,"FRESH_ALTERNATIVE_FROM_LOW_SCORES"){return TextResponse{},errors.New("new team report omitted fresh exploration")}
			return TextResponse{Text:v9Reply("CONTRIBUTE 1",fmt.Sprintf("Synthetic report round %d, preserving attribution and unresolved alternatives.",number),0.8,0.7)},nil
		case "team_assessment":
			if number==1{return TextResponse{Text:v10ContinuationProfile("0.2","FIRST_LOW_SCORE_EVIDENCE: initial fixture approach does not meet the declared targets.")},nil}
			return TextResponse{Text:v10ContinuationProfile("1","NEW_FIXTURE_EVIDENCE: the injected alternative satisfies these synthetic fixture targets.")},nil
		case "queen_return":
			reports,ok:=input["reports"].([]any);if !ok||len(reports)!=1{return TextResponse{},errors.New("Queen lost the current attributed report")}
			report:=reports[0].(map[string]any)
			if number==1{firstReport=id(report["id"]);return TextResponse{Text:v10ContinueReply(firstReport)},nil}
			finalReport=id(report["id"])
			if finalReport==firstReport||!strings.Contains(text,"NEW_FIXTURE_EVIDENCE"){return TextResponse{},errors.New("Queen reused the stale report/profile after new work")}
			return TextResponse{Text:v10ConvergenceReturn(finalReport,"MET")},nil
		case "exploration":
			if input["team_report"]==nil||input["team_assessment"]==nil||!strings.Contains(text,"FIRST_LOW_SCORE_EVIDENCE")||!strings.Contains(text,"Score: 0.2"){return TextResponse{},errors.New("continued worker did not receive the actual low profile and its evidence")}
			if str(input["phase"])=="expand"{return TextResponse{Text:v9Reply("PASS","The candidate has been revised; preserve the saved change.",0.8,0.7)},nil}
			return TextResponse{Text:v9Reply("CONTRIBUTE 1","FRESH_ALTERNATIVE_FROM_LOW_SCORES: change the explanation in response to the saved low assessment, rather than repeat it.",0.9,0.8)},nil
		case "monitor":
			return TextResponse{Text:v9Reply("BEGIN_COLLAPSE","The fresh alternative should now receive another attributed assessment.",0.8,0.7)},nil
		}
		return TextResponse{},fmt.Errorf("unexpected purpose %q",purpose)
	})
	result,err:=ResumeWorkerExperiment(v10Context(t),config,dir,client)
	if err!=nil||result.Status!="returned"||result.CriteriaMet==nil||!*result.CriteriaMet{t.Fatalf("continuation did not finish the new assessed alternative: %+v error=%v",result,err)}
	mu.Lock();reports,assessments,decisions,exploration:=counts["team_report"],counts["team_assessment"],counts["queen_return"],counts["exploration"];totalCalls:=0;for _,n:=range counts{totalCalls+=n};mu.Unlock()
	if reports!=2||assessments!=2||decisions!=2||exploration==0{t.Fatalf("wrong lifecycle: reports=%d profiles=%d decisions=%d exploration=%d",reports,assessments,decisions,exploration)}
	if result.Calls!=totalCalls{t.Fatalf("controller lost call accounting across continuation: result=%d provider=%d",result.Calls,totalCalls)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT'")!=2||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'")!=2{t.Fatal("continuation erased assessment history or reused stale artifacts")}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_guidance WHERE request_id=? AND body=?",h.requestID,v10ContinueReply(firstReport))!=1{t.Fatal("Queen continuation evidence was not preserved exactly")}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT' AND body LIKE ?","%FIRST_LOW_SCORE_EVIDENCE%")!=1{t.Fatal("original low profile was overwritten")}
	if finalReport<=firstReport{t.Fatal("returned report did not originate after the first low profile")}
	t.Logf("low report %d -> saved guidance -> fresh worker contribution -> new report %d and profile -> MET return; old low assessment preserved",firstReport,finalReport)
}

func TestV010BlockedOutcomePreservesUnmetEvidenceWithoutAutomaticRetry(t *testing.T) {
	h,config,dir:=v10Fixture(t,2,nil)
	config.Dashboard.Enabled=false
	v10ConvergenceTeam(t,h,2)
	var reportID int64
	client:=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
		var input map[string]any;if err:=json.Unmarshal([]byte(str(request.Messages[1]["content"])),&input);err!=nil{return TextResponse{},err}
		switch str(input["purpose"]){
		case "team_report":return TextResponse{Text:v9Reply("CONTRIBUTE 1","Unmet fixture candidate with an external dependency.",0.8,0.7)},nil
		case "team_assessment":return TextResponse{Text:v10ContinuationProfile("0.2","External measurement absent; the uncertainty remains explicit.")},nil
		case "queen_return":reportID=id(input["reports"].([]any)[0].(map[string]any)["id"]);return TextResponse{Text:v10ConvergenceBlocked(reportID,"UNMET")},nil
		}
		return TextResponse{},errors.New("blocked fixture unexpectedly explored")
	})
	first,err:=ResumeWorkerExperiment(v10Context(t),config,dir,client)
	if err!=nil||first.Status!="blocked"||first.Calls!=3||first.CriteriaMet==nil||*first.CriteriaMet{t.Fatalf("blocked was treated as a success: %+v %v",first,err)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='blocked' AND result=? AND criteria_met=0",h.requestID,v10ConvergenceBlocked(reportID,"UNMET"))!=1{t.Fatal("explicit blocker/needed action or unmet status was not retained")}
	// Reading the saved decision is inert. A separate explicit work/resume
	// invocation is allowed to reopen exploration after the blocker is resolved.
	reused,err:=h.CompleteWorkerRequest(v10Context(t),v9TextFunc(func(context.Context,TextRequest)(TextResponse,error){t.Error("blocked decision readback called a provider");return TextResponse{},errors.New("must not call")}))
	if err!=nil||reused.Status!="blocked"||reused.Calls!=0||reused.Answer!=first.Answer{t.Fatalf("blocked readback failed: %+v %v",reused,err)}
	t.Log("controller stopped on the explicit blocker; decision readback retained alternatives limitation and needed input with zero further calls")
}

func TestV010ContinueGuidanceAndRequestTransitionRollbackTogether(t *testing.T) {
	h,_,_:=v10Fixture(t,2,nil)
	v10ConvergenceTeam(t,h,2)
	reportID:=v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'convergence','TEAM_REPORT',?,0.8,0.7,?)",v9Reply("CONTRIBUTE 1","Saved report needing improvement.",0.8,0.7),now())
	v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,2,'convergence','TEAM_ASSESSMENT',?,0.8,0.7,?)",v10ConvergenceAssessment("0.2"),now())
	v10Exec(t,h,"UPDATE hive_worker_queen SET note=? WHERE request_id=?","Original note retained on failure.",h.requestID)
	// Reject the later guidance insert, after the transaction's request update.
	v10Exec(t,h,`CREATE TRIGGER reject_continuation BEFORE INSERT ON hive_worker_guidance BEGIN SELECT RAISE(ABORT,'reject continuation transition'); END`)
	result,err:=h.CompleteWorkerRequest(v10Context(t),v9TextFunc(func(context.Context,TextRequest)(TextResponse,error){return TextResponse{Text:v10ContinueReply(reportID)},nil}))
	if err==nil||!strings.Contains(err.Error(),"reject continuation transition")||result.Calls!=1{t.Fatalf("expected atomic continuation failure: %+v %v",result,err)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_guidance")!=0||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='converging'",h.requestID)!=1||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_queen WHERE request_id=? AND note=? AND owner=''",h.requestID,"Original note retained on failure.")!=1{t.Fatal("failed continuation partially wrote guidance, Queen note, state or ownership")}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind IN ('TEAM_REPORT','TEAM_ASSESSMENT')")!=2{t.Fatal("transition rollback discarded prior successful evidence")}
}

func TestV010HistoricalUnmetReturnRemainsReadOnly(t *testing.T) {
	h,config,dir:=v10Fixture(t,2,nil)
	config.Dashboard.Enabled=false
	original:=v10ConvergenceReturn(1,"UNMET")
	v10Exec(t,h,"UPDATE hive_requests SET status='returned',result=?,criteria_met=0 WHERE id=?",original,h.requestID)
	result,err:=ResumeWorkerExperiment(v10Context(t),config,dir,v9TextFunc(func(context.Context,TextRequest)(TextResponse,error){t.Error("historical return triggered a new call");return TextResponse{},errors.New("must not call")}))
	if err!=nil||result.Status!="returned"||result.Calls!=0||result.CriteriaMet==nil||*result.CriteriaMet{t.Fatalf("historical unmet return was rewritten or retried: %+v %v",result,err)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND result=? AND status='returned'",h.requestID,original)!=1{t.Fatal("historical exact return changed")}
}

func TestV010ReassessmentRefreshesForIncomingConnectionOnlyWhenEvidenceChanges(t *testing.T) {
	reports,assessments:=0,0
	client:=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
		text:=str(request.Messages[1]["content"])
		var input map[string]any;if err:=json.Unmarshal([]byte(text),&input);err!=nil{return TextResponse{},err}
		switch str(input["purpose"]){
		case "team_report":
			reports++
			if reports==2&&!strings.Contains(text,"NEW_INCOMING_CONNECTION_EVIDENCE"){return TextResponse{},errors.New("refreshed report did not receive incoming connection")}
			return TextResponse{Text:v9Reply("CONTRIBUTE 1",fmt.Sprintf("Report version %d based on available operational evidence.",reports),0.8,0.7)},nil
		case "team_assessment":assessments++;return TextResponse{Text:v10ContinuationProfile("0.2",fmt.Sprintf("Evidence profile for report version %d.",reports))},nil
		}
		return TextResponse{},errors.New("team-cache fixture called the Queen or explored")
	})
	h,_,_:=v10Fixture(t,2,client)
	v10ConvergenceTeam(t,h,2)
	criteria,err:=workerHandoffCriteria(v9Handoff);if err!=nil{t.Fatal(err)}
	team:=InquiryTeam{InquiryID:1,Members:[]int64{1,2},Contributors:[]int64{1,2}}
	first,err:=h.completeWorkerTeam(v10Context(t),v9Handoff,criteria,team);if err!=nil{t.Fatal(err)}
	v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(1,1,'expand','DISCOVER',?,0.8,0.7,?)","Browse-only activity does not change the candidate evidence.",now())
	unchanged,err:=h.completeWorkerTeam(v10Context(t),v9Handoff,criteria,team)
	if err!=nil||unchanged.ID!=first.ID||unchanged.AssessmentID!=first.AssessmentID||reports!=1||assessments!=1{t.Fatalf("browse-only activity replayed completed scoring: %+v counts=%d/%d err=%v",unchanged,reports,assessments,err)}
	source:=v10Exec(t,h,"INSERT INTO hive_inquiries(request_id,question,proposed_by,created) VALUES(?,?,?,?)",h.requestID,"A related inquiry",1,now())
	v10Exec(t,h,"INSERT INTO hive_inquiry_members(inquiry_id,drone_id,commitment,created) VALUES(?,1,?,?)",source,"Source commitment",now())
	v10Exec(t,h,"INSERT INTO hive_inquiry_work(inquiry_id,target_inquiry_id,drone_id,phase,kind,body,relevance,inquiry_value,created) VALUES(?,1,1,'expand','CONNECT',?,0.8,0.7,?)",source,"NEW_INCOMING_CONNECTION_EVIDENCE",now())
	updated,err:=h.completeWorkerTeam(v10Context(t),v9Handoff,criteria,team)
	if err!=nil||updated.ID<=first.ID||updated.AssessmentID<=first.AssessmentID||reports!=2||assessments!=2{t.Fatalf("new connection reused stale report/profile: %+v counts=%d/%d err=%v",updated,reports,assessments,err)}
	stable,err:=h.completeWorkerTeam(v10Context(t),v9Handoff,criteria,team)
	if err!=nil||stable.ID!=updated.ID||stable.AssessmentID!=updated.AssessmentID||reports!=2||assessments!=2{t.Fatalf("new report/assessment invalidated themselves: %+v counts=%d/%d err=%v",stable,reports,assessments,err)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT'")!=2||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'")!=2{t.Fatal("refresh did not preserve both attributed generations")}
	t.Logf("incoming connection refreshed report %d -> %d; DISCOVER and repeated reads reused completed profiles",first.ID,updated.ID)
}
