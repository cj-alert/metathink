package main

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type v10PhaseLogFunc func(string,string,map[string]any)

func (f v10PhaseLogFunc) Log(scope,event string,fields map[string]any) { f(scope,event,fields) }

func TestV010DrainedPoolPreservesAnotherControllersCompletedPhase(t *testing.T) {
	h,config,dir:=v10Fixture(t,2,nil)
	h.Config.Dashboard.Enabled=false
	external:=v10Peer(t,h,config,dir)
	ctx:=v10Context(t)
	original:=v10ConvergenceReturn(1,"UNMET") // A preserved historical return.
	var once sync.Once
	var externalErr error
	committed:=false
	sink:=h.Store.log
	// Use the existing diagnostic boundary to order two real SQLite owners;
	// no production hook or timing-dependent sleep is introduced.
	h.Store.log=v10PhaseLogFunc(func(scope,event string,fields map[string]any){
		sink.Log(scope,event,fields)
		if event=="sql_start"&&fields["sql_op"]=="final_worker_state"{
			once.Do(func(){
				_,externalErr=external.exec(ctx,"fixture_other_controller_return","hive_requests","UPDATE hive_requests SET status='returned',result=?,finish_reason='convergence_returned',criteria_met=0 WHERE id=?",original,h.requestID)
				committed=externalErr==nil
			})
		}
	})
	var mu sync.Mutex
	calls:=0
	client:=v9TextFunc(func(_ context.Context,request TextRequest)(TextResponse,error){
		if request.Model!=config.Models.Drone{return TextResponse{},errors.New("completed-phase routing invoked an unexpected Queen")}
		mu.Lock();calls++;mu.Unlock()
		return TextResponse{Text:v9Reply("PASS","No new work in this fixture.",0.6,0.5)},nil
	})
	result,err:=runWorkerPool(ctx,h,client)
	if externalErr!=nil||!committed{t.Fatalf("fixture did not establish the competing saved phase: committed=%t error=%v",committed,externalErr)}
	if err!=nil||result.Status!="returned"||result.CriteriaMet==nil||*result.CriteriaMet{t.Fatalf("drained controller did not route the saved terminal state: %+v error=%v",result,err)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_requests WHERE id=? AND status='returned' AND result=? AND finish_reason='convergence_returned' AND criteria_met=0",h.requestID,original)!=1{t.Fatal("an obsolete pool finalizer overwrote the newer completed phase")}
	mu.Lock();actualCalls:=calls;mu.Unlock()
	if actualCalls!=4||result.Calls!=actualCalls{t.Fatalf("terminal phase caused replay or lost existing calls: provider=%d result=%d",actualCalls,result.Calls)}
	if v9Count(t,h.Store.db,"SELECT COUNT(*) FROM hive_worker_queue WHERE owner<>''")!=0{t.Fatal("drained pool retained an actor claim")}
	t.Log("a second SQLite controller committed RETURN at the drained-state read boundary; the older pool preserved and returned it with no replay")
}
