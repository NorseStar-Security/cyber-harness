package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestObservationReplayPreservesSamplingAndInputIdentity(t *testing.T) {
	r := observationReflex(t, `js:function(){return {report:{omitted:omitted_evidence}};}`)
	messages := []map[string]any{{"role": "user", "text": "Current task"}}
	for i := range 25 {
		messages = append(messages, map[string]any{"role": "tool", "call_id": fmt.Sprint(i), "text": "Actual result"})
	}
	for _, omitted := range []int{0, 3} {
		state, _ := json.Marshal(map[string]any{"messages": messages, "omitted_evidence": omitted})
		replay, err := newObservationReplay(&r, state, observationCapabilities())
		if err != nil {
			t.Fatal(err)
		}
		wantRecent := append([]int{1}, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26)
		if got := replay.boundaries(true); !reflect.DeepEqual(got, wantRecent) {
			t.Fatalf("verification sample changed: %v", got)
		}
		if err := replay.verify(t.Context()); err != nil {
			t.Fatal(err)
		}
		before := len(replay.cache)
		witnesses, err := replay.witnesses(t.Context())
		if err != nil || len(witnesses) != 16 || witnesses[0]["boundary"] != 1 || witnesses[15]["boundary"] != 16 {
			t.Fatalf("witness sample changed: witnesses=%v error=%v", witnesses, err)
		}
		wantAdditional := 10
		if omitted != 0 {
			wantAdditional = 16
		}
		if len(replay.cache)-before != wantAdditional {
			t.Fatalf("distinct observation inputs were confused: before=%d after=%d", before, len(replay.cache))
		}
		if !strings.Contains(string(witnesses[0]["state"].(json.RawMessage)), `"omitted":0`) {
			t.Fatal("witness inherited verification-only omitted evidence")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := replay.evaluate(ctx, replay.input(1, true)); err == nil {
			t.Fatal("cached observation bypassed cancellation")
		}
	}
}

func TestCompilerAllowsHonestPartialSceneWithoutEntryBinding(t *testing.T) {
	r := Program{When: "Current native resource workflow", Decide: "Inspect known resources, defer until a handle is available", Observe: executableFixture(`js:(() => {
const recent = history.length ? history[history.length-1] : null;
const handle = recent && recent.data && recent.data.handle;
return {state:{handle:handle || null},candidates:choices(handle ? [bind(tools[0].name,{handle:handle},true)] : [])};
})()`)}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"messages":[{"role":"user","text":"Inspect a resource"},{"role":"assistant","calls":[{"id":"open","name":"native","arguments":{}}]},{"role":"tool","call_id":"open","text":"{\"handle\":\"current\"}"}]}`)
	replay, err := newObservationReplay(&r, input, map[string]any{"tools": []any{map[string]any{"name": "native"}}, "commands": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.verify(t.Context()); err != nil {
		t.Fatalf("useful partial scene cannot reach semantic review: %v", err)
	}
}

func TestFreshnessReviewRetainsConcreteRejectionWithEitherGlobalVerdict(t *testing.T) {
	for _, verdict := range []string{"compile", Defer} {
		t.Run(verdict, func(t *testing.T) {
			requests, checked := 0, false
			client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
				requests++
				out := declarationAnswers(req, true)
				if _, exists := req.Questions["coverage_freshness"]; exists {
					checked = true
					out["compile"] = answer(verdict)
					out["coverage_freshness"] = answer(Defer)
				}
				return out
			})
			e, _, _ := testInstallation(t, Config{Mode: "auto"}, client)
			r := observationReflex(t, `js:function(context,args){const r=execute({name:'native',arguments:{},read:false,step:"effect",occurrence:0});return {report:r.data};}`)
			caps := observationCapabilities("native")
			state := json.RawMessage(`{"messages":[{"role":"user","text":"Inspect fresh state"}]}`)
			replay, err := newObservationReplay(&r, state, caps)
			if err != nil {
				t.Fatal(err)
			}
			witnesses, err := replay.witnesses(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = e.reviewReflex(t.Context(), &r, &compilation{capabilities: caps, state: state}, witnesses)
			if !checked || requests != 1 || err == nil {
				t.Fatalf("concrete rejection lost or redundant diagnosis requested: checked=%t requests=%d err=%v", checked, requests, err)
			}
			diagnostic := compilerDiagnostic(err)
			if diagnostic.Code != "native_access_invalid" || diagnostic.Stage != "semantic" || !strings.Contains(diagnostic.Action, "helper") {
				t.Fatalf("concrete structured repair lost: %+v", diagnostic)
			}
		})
	}
}

func TestReviewRetainsBoundaryRejectionWithGlobalDefer(t *testing.T) {
	requests := 0
	client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
		requests++
		out := declarationAnswers(req, true)
		out["compile"], out["coverage0"] = answer(Defer), answer(Defer)
		return out
	})
	e, _, _ := testInstallation(t, Config{Mode: "auto"}, client)
	r := observationReflex(t, `js:function(){return{defer:'missing next operation'};}`)
	boundary := map[string]any{"boundary": 2, "state": map[string]any{"current": "actual"}, "candidates": map[string]coretool.NativeCall{}, "next_calls": []any{map[string]any{"name": "native", "arguments": map[string]any{"operation": "required"}}}}
	err := e.reviewReflex(t.Context(), &r, &compilation{state: json.RawMessage(`{"messages":[]}`)}, []map[string]any{boundary})
	if err == nil {
		t.Fatal("rejected boundary accepted")
	}
	diagnostic := compilerDiagnostic(err)
	if requests != 1 || diagnostic.Code != "semantic_validation_failed" || !strings.Contains(jsonText(diagnostic.Expected), "required") {
		t.Fatalf("concrete boundary lost: requests=%d diagnostic=%+v", requests, diagnostic)
	}
}

func TestResultReviewDistinguishesCompletionFromReadClassification(t *testing.T) {
	requests := 0
	client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
		requests++
		out := declarationAnswers(req, true)
		out["compile"], out["coverage_result"] = answer(Defer), answer(Defer)
		out["coverage_freshness"] = answer("compile")
		return out
	})
	e, _, _ := testInstallation(t, Config{Mode: "auto"}, client)
	r := observationReflex(t, `js:function(){const r=execute({name:'native',arguments:{},read:true});return{report:'done'};}`)
	witness := map[string]any{"boundary": 1, "report": "done", "candidates": map[string]coretool.NativeCall{"read": {Name: "native", Arguments: json.RawMessage(`{}`), Read: true}}}
	err := e.reviewReflex(t.Context(), &r, &compilation{capabilities: observationCapabilities("native"), state: json.RawMessage(`{"messages":[]}`)}, []map[string]any{witness})
	diagnostic := compilerDiagnostic(err)
	if requests != 1 || diagnostic.Code != "semantic_validation_failed" || diagnostic.Stage != "completion" || !strings.Contains(jsonText(diagnostic.Actual), "done") {
		t.Fatalf("completion defect was misclassified or its output evidence lost: requests=%d diagnostic=%+v", requests, diagnostic)
	}
}

func TestReviewSharesBindingsWithoutLosingActualBoundaryEvidence(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"program": strings.Repeat("native program ", 1000), "version": json.Number("9007199254740993")})
	candidate := coretool.NativeCall{Name: "ordinary", Arguments: args, Read: true}
	var witnesses []map[string]any
	for i := 0; i < 8; i++ {
		witnesses = append(witnesses, map[string]any{"boundary": i, "latest": fmt.Sprintf("actual result %d", i), "candidates": map[string]coretool.NativeCall{"current": candidate}, "next_calls": []string{fmt.Sprintf("next %d", i)}})
	}
	raw, _ := json.Marshal(witnesses)
	rows, bindings := compactWitnesses(witnesses)
	compact, _ := json.Marshal(map[string]any{"evaluations": rows, "bindings": bindings})
	if len(raw) <= 64<<10 || len(compact) >= 32<<10 || len(rows) != 8 || len(bindings) != 1 {
		t.Fatalf("duplicate readers exceed review budget: raw=%d compact=%d rows=%d bindings=%d", len(raw), len(compact), len(rows), len(bindings))
	}
	for i, row := range rows {
		ref := row["candidates"].(map[string]string)["current"]
		if canonicalBinding := bindings[ref]; canonicalBinding.Name != candidate.Name || string(canonicalBinding.Arguments) != string(args) || !canonicalBinding.Read || row["latest"] != witnesses[i]["latest"] || row["next_calls"] == nil {
			t.Fatal("compaction changed an actual binding or its boundary evidence")
		}
		if _, ok := witnesses[i]["candidates"].(map[string]coretool.NativeCall); !ok {
			t.Fatal("compaction mutated original evidence")
		}
	}
}

func TestReviewSharesActualResultsAcrossReportBoundaries(t *testing.T) {
	const payload = "native-result-" + "complete-data:"
	data := strings.Repeat(payload, 350)
	requests := 0
	client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
		requests++
		if len(req.State) >= 64<<10 || strings.Count(string(req.State), data) != 4 || !strings.Contains(string(req.State), "Current task constraints") || !strings.Contains(string(req.State), "Earlier user requirement") || strings.Contains(string(req.State), "Control receipt") {
			t.Errorf("review evidence: bytes=%d result copies=%d constraints=%t", len(req.State), strings.Count(string(req.State), data), strings.Contains(string(req.State), "Current task constraints"))
		}
		return declarationAnswers(req, true)
	})
	e, _, _ := testInstallation(t, Config{Mode: "auto"}, client)
	caps := observationCapabilities("native")
	r := observationReflex(t, `js:function(){let result;for(let i=0;i<4;i++)result=execute({name:'native',arguments:{index:i},read:true});return{report:{evidence:result.call_id,path:['data']}};}`)
	messages := []any{
		map[string]any{"role": "system", "text": "Current task constraints"},
		map[string]any{"role": "user", "text": "Earlier user requirement"},
		map[string]any{"role": "user", "name": "control", "text": "Control receipt"},
		map[string]any{"role": "user", "text": "Inspect the current results and report actual data."},
	}
	for i := range 4 {
		id := fmt.Sprint(i)
		messages = append(messages, map[string]any{"role": "assistant", "calls": []any{map[string]any{"id": id, "name": "native", "arguments": map[string]any{"index": i}}}}, map[string]any{"role": "tool", "call_id": id, "data": map[string]any{"payload": data}})
	}
	state := json.RawMessage(jsonText(map[string]any{"messages": messages}))
	replay, err := newObservationReplay(&r, state, caps)
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.verify(t.Context()); err != nil {
		t.Fatal(err)
	}
	witnesses, err := replay.witnesses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, witness := range witnesses {
		if strings.Contains(string(witness["state"].(json.RawMessage)), `"diagnostic"`) {
			t.Fatal("semantic evidence duplicated mechanism repair diagnostics")
		}
	}
	if err := e.reviewReflex(t.Context(), &r, &compilation{state: state, capabilities: caps}, witnesses); err != nil || requests != 1 {
		t.Fatalf("complete evidence could not reach semantic review: requests=%d err=%v", requests, err)
	}
	r.Observe = `js:function(){return{report:{evidence:'missing',path:['data']}};}`
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	replay, err = newObservationReplay(&r, state, caps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.witnesses(t.Context()); err == nil {
		t.Fatal("retaining references bypassed report provenance validation")
	}
}

func TestCompilerChecksEarlierBoundariesAndNativeRequiredArguments(t *testing.T) {
	caps := observationCapabilities("native")
	caps["tools"].([]any)[0].(map[string]any)["input_schema"] = map[string]any{"type": "object", "required": []any{"selector"}, "properties": map[string]any{"selector": map[string]any{"type": "string", "minLength": 1}}}
	input := json.RawMessage(`{"messages":[{"role":"user","text":"Select something"},{"role":"assistant","calls":[{"id":"read","name":"native","arguments":{"selector":"current"}}]},{"role":"tool","call_id":"read","text":"{\"complete\":true}"}]}`)
	for _, code := range []string{
		`js:function(context,args){return {report:history[0].data};}`,
		`js:function(context,args){execute({name:'native',arguments:{selector:context.user.split('Select ')[2]},read:false,step:"effect",occurrence:0});return {report:'done'};}`,
	} {
		r := observationReflex(t, code)
		replay, err := newObservationReplay(&r, input, caps)
		if err != nil {
			t.Fatal(err)
		}
		if err := replay.verify(t.Context()); err == nil {
			t.Fatal("invalid earlier boundary or native arguments were admitted")
		}
	}
}
