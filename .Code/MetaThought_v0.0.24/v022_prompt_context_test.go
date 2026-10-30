package main

import (
 "context"
 "encoding/json"
 "io"
 "net/http"
 "strings"
 "testing"
)

type v22Transport func(*http.Request)(*http.Response,error)
func(f v22Transport)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}
func v22Config(t *testing.T)Config{t.Helper();c,err:=LoadConfig("config.json");if err!=nil{t.Fatal(err)};return c}
func v22Input()map[string]any{
 research:=jsonText(map[string]any{"question":"Compare the complete formulation", "status":"incomplete", "need":"One source retrieval failed", "answer":"Supporting result AND decisive contrary finding. Uncertainty high. https://source.test/paper", "search_calls":[]any{map[string]any{"id":"PROVIDER_SEARCH_ID_NOT_NEEDED", "status":"failed", "results":[]any{map[string]any{"url":"https://source.test/raw-result"}}}}, "citations":[]any{map[string]any{"url":"https://source.test/citation"}}})
 row:=map[string]any{"id":14,"kind":"RESEARCH","drone_id":3,"body":research,"created":"2026-10-29T12:00:00Z","relevance":0.9,"inquiry_value":0.7}
 return map[string]any{"handoff":"Exact accepted contract\r\nNovelty >= 1.0\r\nTestability > 0.35\r\n", "inquiry":map[string]any{"id":4,"question":"Original hypothesis"},"report":map[string]any{"id":20,"drone_id":1,"body":"Candidate claim; a material contrary finding remains."},"research_history":[]any{row},"research_work":row,"work":[]any{row},"connections":[]any{map[string]any{"body":"HISTORICAL_CONNECTION_LOG_NOT_NEEDED"}},"scoring_elements":[]any{map[string]any{"name":"Novelty","target":map[string]any{"operator":">=","value":1.0}},map[string]any{"name":"Testability","target":map[string]any{"operator":">","value":0.35}}}}
}
func TestV022ProjectionPreservesJudgmentEvidenceAndOriginals(t *testing.T){
 c:=v22Config(t);data:=v22Input();original:=jsonText(data)
 got:=promptContextText(original,"worker_team_assess",c.Meta.PromptContext,false)
 for _,want:=range []string{str(data["handoff"]),"Original hypothesis","Candidate claim","decisive contrary finding","Uncertainty high","incomplete","One source retrieval failed","id: 14","drone_id: 3","Novelty","Testability","0.35","operator:\n>","relevance: 0.9","inquiry_value: 0.7"}{if !strings.Contains(got,want){t.Fatalf("lost required context %q in %s",want,got)}}
 for _,unwanted:=range []string{"https://","search_calls","PROVIDER_SEARCH_ID_NOT_NEEDED","HISTORICAL_CONNECTION_LOG_NOT_NEEDED","\"body\""}{if strings.Contains(got,unwanted){t.Fatalf("unnecessary payload leaked: %s",unwanted)}}
 if strings.Count(got,"Supporting result")!=1{t.Fatal("research is repeated")}
 if jsonText(data)!=original{t.Fatal("formatting changed the original records")}
	// Completed report objects carry the drone-owned assessments beyond their body.
	returned := promptContextText(jsonText(map[string]any{"reports": []any{map[string]any{"id": 30, "body": "Candidate report", "assessor_id": 7, "assessments": []any{map[string]any{"name": "Novelty", "score": 0.2, "reason": "Equivalent prior work exists", "evidence": "Contrary source retained"}}, "target_statuses": []any{map[string]any{"name": "Novelty", "met": false}}}}}), "worker_queen_return", c.Meta.PromptContext, false)
	for _, want := range []string{"assessments:", "score:\n0.2", "Equivalent prior work exists", "Contrary source retained", "assessor_id:\n7", "met:\nfalse"} { if !strings.Contains(returned, want) { t.Fatalf("Queen return lost actual drone profile %q: %s", want, returned) } }
 question:=`{"exact user data":[1,2],"source":"https://user.test/question"}`
 intake:=promptContextText(jsonText(map[string]any{"question":question,"feedback":"Exact feedback"}),"queen_intake",c.Meta.PromptContext,false)
 if !strings.Contains(intake,question)||!strings.Contains(intake,"Exact feedback"){t.Fatal("exact user input changed")}
 method:=promptContextText(jsonText(map[string]any{"method":map[string]any{"expand":"Try a different causal representation"},"actual_scores":map[string]any{"contribute":0.4,"expand":0.8},"uncertainty":"Correlation is not reciprocal influence"}),"mq_assess",c.Meta.PromptContext,false)
 for _,want:=range []string{"Try a different causal representation","contribute:\n0.4","expand:\n0.8","Correlation is not reciprocal influence"}{if !strings.Contains(method,want){t.Fatalf("meta context lost %q",want)}}
}
func TestV022MetaWireUsesPlainContextAndResearchKeepsURLs(t *testing.T){
 c:=v22Config(t);t.Setenv("METATHOUGHT_V022_KEY","fixture-key");c.Meta.APIKeyFile="";c.Meta.APIKeyEnv="METATHOUGHT_V022_KEY";c.Meta.BaseURL="https://v022.invalid/v1"
 client,err:=NewMetaClient(c.Meta,&metaTestLog{});if err!=nil{t.Fatal(err)}
 var wire map[string]any
 client.HTTP.Transport=v22Transport(func(r *http.Request)(*http.Response,error){
  if err:=json.NewDecoder(r.Body).Decode(&wire);err!=nil{t.Fatal(err)}
  reply:=`{"model":"fixture","choices":[{"finish_reason":"stop","message":{"content":"Scored result\n0.9\n0.8"}}]}`
  if strings.HasSuffix(r.URL.Path,"/responses"){reply=`{"model":"fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Finding\n0.9\n0.8"}]}]}`}
  return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(reply))},nil
 })
 original:=jsonText(v22Input());request:=TextRequest{Model:"fixture",Scope:"drone",ContextKind:"worker_team_assess",Messages:[]Message{{"role":"developer","content":"Keep exact scoring instructions"},{"role":"user","content":original}}}
 if _,err=client.Complete(context.Background(),request);err!=nil{t.Fatal(err)}
 messages:=wire["messages"].([]any);plain:=str(messages[1].(map[string]any)["content"])
 if json.Valid([]byte(plain))||strings.Contains(plain,"search_calls")||!strings.Contains(plain,"decisive contrary finding"){t.Fatal("wire did not receive projected evidence")}
 if str(request.Messages[1]["content"])!=original||str(messages[0].(map[string]any)["content"])!="Keep exact scoring instructions"{t.Fatal("original or developer instruction mutated")}
 request.ContextKind="worker_research";request.WebSearch=true;request.Messages[1]=Message{"role":"user","content":jsonText(map[string]any{"question":"Open https://source.test/needed","evidence":"Find the source at https://source.test/also-needed"})}
 if _,err=client.Complete(context.Background(),request);err!=nil{t.Fatal(err)}
 if !strings.Contains(jsonText(wire["input"]),"https://source.test/needed")||!strings.Contains(jsonText(wire["input"]),"https://source.test/also-needed"){t.Fatal("web call lost required URLs")}
 for _,key:=range []string{"max_tokens","max_completion_tokens","max_output_tokens"}{if _,ok:=wire[key];ok{t.Fatal("context formatting introduced a token ceiling")}}
}
func TestV022DecisionsPrivateContextIsProjectedAndPublicInputIsExact(t *testing.T){
 c:=v22Config(t);t.Setenv("METATHOUGHT_V022_KEY","fixture-key");c.Meta.APIKeyFile="";c.Meta.APIKeyEnv="METATHOUGHT_V022_KEY";c.Meta.BaseURL="https://v022.invalid/v1"
 client,err:=NewMetaClient(c.Meta,&metaTestLog{});if err!=nil{t.Fatal(err)}
 var captured []string
 client.HTTP.Transport=v22Transport(func(r *http.Request)(*http.Response,error){
  var wire map[string]any;if err:=json.NewDecoder(r.Body).Decode(&wire);err!=nil{t.Fatal(err)}
  parts:=wire["messages"].([]any)[1].(map[string]any)["content"].([]any)
  captured=append(captured,str(parts[1].(map[string]any)["text"]))
  return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(`{"model":"fixture","choices":[{"finish_reason":"stop","message":{"content":"1\n0.9\n0.1\n0.8\n0.9\n0.8"}}]}`))},nil
 })
 decisions:=NewMetaDecisions(client,c.Decisions,&metaTestLog{})
 text:=jsonText(map[string]any{"queen_reasoning":map[string]any{"body":"Guide the inquiry; do not collect formal reports yet","relevance":0.9,"inquiry_value":0.8},"operational_evidence":map[string]any{"search_calls":"UNNEEDED_REPEATED_CONTEXT","url":"https://public.test/exact"}})
 request:=DecisionRequest{Model:"fixture",Input:raw(text),ContextKind:"worker_queen_route",Questions:[]DecisionQuestion{{Type:"choice",Instructions:raw("Choose the fitting action"),Choices:[]DecisionChoice{{Value:"GUIDE"},{Value:"BEGIN_COLLAPSE"}}}}}
 answer,err:=decisions.Decide(context.Background(),request);if err!=nil||len(answer.Answers)!=1{t.Fatalf("answer=%+v error=%v",answer,err)}
 if !strings.Contains(captured[0],"Guide the inquiry")||strings.Contains(captured[0],"UNNEEDED_REPEATED_CONTEXT"){t.Fatal("routing received repeated operational payload")}
 request.ContextKind="";if _,err=decisions.Decide(context.Background(),request);err!=nil{t.Fatal(err)}
 if captured[1]!=text{t.Fatal("public Decisions input was rewritten")}
 encoded:=jsonText(request);if strings.Contains(encoded,"ContextKind")||strings.Contains(encoded,"context_kind"){t.Fatal("private marker leaked into public contract")}
}
