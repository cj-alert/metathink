package main

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "strings"
 "sync/atomic"
 "testing"
 "time"
)
func v23Reply(body string)string{return "# YOUR ANSWER\n"+body+"\n# YOUR SCORE\nrelevance: 0.9\ninquiry_value: 0.8\n"}
func TestV023InterimKeepsCurrentThinkerRunning(t *testing.T){
 h,c,dir:=v10Fixture(t,2,nil);v10ConvergenceTeam(t,h,2);v10Exec(t,h,"UPDATE hive_requests SET status='working' WHERE id=?",h.requestID)
 h.Config.Workers.Count=2;c.Workers.Count=2
 claim,err:=h.ClaimDrone(v10Context(t),"active-thinker",2);if err!=nil||claim==nil{t.Fatal(err)}
 started,release,done:=make(chan struct{}),make(chan struct{}),make(chan error,1)
 var thinking atomic.Bool
 fake:=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
  if request.ContextKind=="inquiry_contribute"{thinking.Store(true);close(started);<-release;thinking.Store(false);return TextResponse{Text:v23Reply("Ongoing thought completed")},nil}
  if request.ContextKind!="worker_queen_interim"{return TextResponse{},fmt.Errorf("unexpected call %s",request.ContextKind)}
  if !thinking.Load(){return TextResponse{},errors.New("interim stopped the thinker")}
  if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE status='working'")!=1{return TextResponse{},errors.New("interim changed active state")}
  return TextResponse{Text:v23Reply("Best current candidate remains unassessed; uncertainty preserved")},nil
 })
 peer:=v10Peer(t,h,c,dir);peer.Text.Inner=fake;h.Text.Inner=fake
 go func(){_,e:=peer.generatePrompt(context.Background(),"drone",c.Models.Drone,"inquiry_contribute","Contribute with actual local scores","ongoing thought","");done<-e}()
 <-started
 defer func(){select{case<-release:default:close(release)}}()
 answer,err:=h.submitWorkerAnswer(v10Context(t),"interim");if err!=nil{t.Fatal(err)}
 other,err:=h.ClaimDrone(v10Context(t),"new-thinker",2);if err!=nil||other!=nil{t.Fatal("pending user answer did not receive priority")}
 continued,err:=h.ClaimDrone(v10Context(t),"active-thinker",2);if err!=nil||continued==nil||continued.DroneID!=claim.DroneID{t.Fatal("pending answer blocked an already-owned thinker")}
 got,err:=h.MonitorWorkerQueen(v10Context(t),"interim-slot");if err!=nil||!got.Called||got.BeginCollapse{t.Fatalf("interim=%+v error=%v",got,err)}
 saved,err:=h.readWorkerAnswer(v10Context(t),answer.ID);if err!=nil||saved.Status!="completed"||saved.Body!=v23Reply("Best current candidate remains unassessed; uncertainty preserved")||saved.CriteriaMet!=nil{t.Fatalf("answer=%+v error=%v",saved,err)}
 if !thinking.Load()||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_queue WHERE owner='active-thinker'")!=1{t.Fatal("interim drained or released the thinker")}
 close(release);if err=<-done;err!=nil{t.Fatal(err)}
 t.Log("One interim Queen call returned while an existing thinker remained active; request and claim stayed open")
}
func TestV023FinishDrainsAndClosesWithUnmetFindings(t *testing.T){
 h,_,_:=v10Fixture(t,2,nil);v10ConvergenceTeam(t,h,2);v10Exec(t,h,"UPDATE hive_requests SET status='working' WHERE id=?",h.requestID)
 claim,err:=h.ClaimDrone(v10Context(t),"finishing-thinker",10);if err!=nil||claim==nil{t.Fatal(err)}
 var calls atomic.Int64
 h.Text.Inner=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
  calls.Add(1)
  if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>''")!=0{return TextResponse{},errors.New("finish did not drain")}
  switch request.ContextKind{
  case "worker_team_report":return TextResponse{Text:v9Reply("CONTRIBUTE 1","Original team finding remains provisional",0.8,0.7)},nil
  case "worker_team_assess":return TextResponse{Text:v10ConvergenceAssessment("0.2")},nil
  case "worker_queen_finish":
   var input map[string]any;if err:=json.Unmarshal([]byte(str(request.Messages[1]["content"])),&input);err!=nil{return TextResponse{},err}
   report:=input["reports"].([]any)[0].(map[string]any);score:=report["assessments"].([]any)[0].(map[string]any)["score"]
   if str(score)!="0.2"{return TextResponse{},errors.New("actual unmet drone score missing")}
   return TextResponse{Text:v23Reply("Best available: provisional team finding. Novelty 0.2 misses target; other criteria and uncertainty remain. User requested closure, not a success claim.")},nil
  };return TextResponse{},fmt.Errorf("unexpected call %s",request.ContextKind)
 })
 ctx:=v10Context(t);type outcome struct{answer WorkerAnswer;err error};finished:=make(chan outcome,1)
 go func(){answer,err:=h.RunWorkerControl(ctx,"final",true);finished<-outcome{answer,err}}()
 for v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE status='converging'")==0{select{case<-ctx.Done():t.Fatal("finish did not request drain");case<-time.After(time.Millisecond):}}
 if calls.Load()!=0{t.Fatal("review began before current call released its claim")}
 fresh,err:=h.ClaimDrone(ctx,"new-after-finish",10);if err!=nil||fresh!=nil{t.Fatal("finish admitted new work")}
 if err=h.ReleaseWorker(ctx,*claim);err!=nil{t.Fatal(err)}
 var result outcome;select{case result=<-finished:case<-ctx.Done():t.Fatal("finish did not return")}
 if result.err!=nil||result.answer.Status!="completed"||!strings.Contains(result.answer.Body,"Novelty 0.2")||result.answer.CriteriaMet!=nil||calls.Load()!=3{t.Fatalf("answer=%+v error=%v calls=%d",result.answer,result.err,calls.Load())}
 if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE status='finished' AND finish_reason='operator_finished' AND criteria_met IS NULL")!=1{t.Fatal("manual finish invented success or remained open")}
 if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_REPORT'")!=1||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_inquiry_work WHERE kind='TEAM_ASSESSMENT'")!=1{t.Fatal("finish skipped full team review")}
 h.Text.Inner=v9TextFunc(func(context.Context,TextRequest)(TextResponse,error){t.Fatal("closed readback called a model");return TextResponse{},errors.New("no repeat")})
 reused,err:=h.RunWorkerControl(ctx,"final",true);if err!=nil||reused.Body!=result.answer.Body{t.Fatal("closed answer did not reuse original")}
 completion,err:=h.CompleteWorkerRequest(ctx,h.Text.Inner);if err!=nil||completion.Status!="finished"||completion.Calls!=0{t.Fatal("closed lifecycle tried exploration or review")}
 t.Log("Finish blocked admissions, waited for owned work, ran report/peer assessment, returned unmet best findings and closed without a false success")
}
func TestV023ContinuousAndAutomaticCompletion(t *testing.T){
 for _,continuous:=range []bool{true,false}{t.Run(fmt.Sprintf("continuous_%t",continuous),func(t *testing.T){
  h,_,_:=v10Fixture(t,2,nil);v10ConvergenceTeam(t,h,2);h.Config.Workers.Continuous=continuous
  h.Text.Inner=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
   var input map[string]any;if err:=json.Unmarshal([]byte(str(request.Messages[1]["content"])),&input);err!=nil{return TextResponse{},err}
   switch request.ContextKind{
   case "worker_team_report":return TextResponse{Text:v9Reply("CONTRIBUTE 1","Supported candidate",0.8,0.7)},nil
   case "worker_team_assess":return TextResponse{Text:v10ConvergenceAssessment("1")},nil
   case "worker_queen_return":report:=input["reports"].([]any)[0].(map[string]any);return TextResponse{Text:v10ConvergenceReturn(id(report["id"]),"MET")},nil
   };return TextResponse{},errors.New("unexpected test call")
  })
  completed,err:=h.CompleteWorkerRequest(v10Context(t),h.Text.Inner);if err!=nil{t.Fatal(err)}
  wantStatus,wantKind:="returned","final";if continuous{wantStatus,wantKind="working","interim"}
  if completed.Status!=wantStatus||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE status=?",wantStatus)!=1||v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_request_answers WHERE status='completed' AND kind=? AND criteria_met=1",wantKind)!=1{t.Fatalf("continuous=%t completion=%+v",continuous,completed)}
  if continuous&&v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_guidance")!=1{t.Fatal("continued thinkers did not receive returned findings")}
 })}
}
func TestV023FinishBeforeTeamsPreservesUnknownAndPendingAsks(t *testing.T){
 h,_,_:=v10Fixture(t,2,nil)
 h.Text.Inner=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){if request.ContextKind!="worker_queen_finish"{return TextResponse{},errors.New("no team exists to grade")};return TextResponse{Text:v23Reply("No supported team conclusion yet. Criteria are unassessed; retained partial findings are not a solution.")},nil})
 interim,err:=h.submitWorkerAnswer(v10Context(t),"interim");if err!=nil{t.Fatal(err)}
 firstFinal,err:=h.submitWorkerAnswer(v10Context(t),"final");if err!=nil{t.Fatal(err)}
 lastFinal,err:=h.submitWorkerAnswer(v10Context(t),"final");if err!=nil{t.Fatal(err)}
 completion,err:=h.CompleteWorkerRequest(v10Context(t),h.Text.Inner);if err!=nil||completion.Status!="finished"||completion.CriteriaMet!=nil||completion.Calls!=1{t.Fatalf("completion=%+v err=%v",completion,err)}
 for _,answerID:=range []int64{interim.ID,firstFinal.ID,lastFinal.ID}{answer,err:=h.readWorkerAnswer(v10Context(t),answerID);if err!=nil||answer.Status!="completed"||!strings.Contains(answer.Body,"unassessed"){t.Fatal("finished parent left a pending user ask")}}
 t.Log("An early user finish closes honestly with unknown criteria and supplies the final answer to queued asks")
}

func TestV023DashboardShowsInterimAndFinalAnswers(t *testing.T){
 h,_,_:=v10Fixture(t,2,nil)
 answerID:=v10Exec(t,h,"INSERT INTO hive_request_answers(request_id,kind,status,body,relevance,inquiry_value,created) VALUES(?,'interim','completed',?,0.9,0.8,?)",h.requestID,v23Reply("Best current answer; uncertainty remains"),now())
 snapshot,err:=h.dashboardSnapshot(v10Context(t));if err!=nil||snapshot.LatestAnswer==nil||snapshot.LatestAnswer.ID!=answerID||snapshot.Status!="working"{t.Fatalf("snapshot=%+v error=%v",snapshot.LatestAnswer,err)}
 html,err:=renderDashboard(snapshot,3);if err!=nil||!strings.Contains(string(html),"current-answer-title")||!strings.Contains(string(html),"Request remains open"){t.Fatal("dashboard omitted the interim distinction")}
 v10Exec(t,h,"UPDATE hive_requests SET status='finished',finish_reason='operator_finished' WHERE id=?",h.requestID)
 snapshot,err=h.dashboardSnapshot(v10Context(t));if err!=nil||snapshot.Status!="finished"||!dashboardFinal(snapshot.Status){t.Fatal("user-finished request remains shown as live")}
}
