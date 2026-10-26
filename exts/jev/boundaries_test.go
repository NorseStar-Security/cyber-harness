package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/decision"
	coretool "github.com/chainreactors/cyber/core/tool"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/provider"
	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/operation"
)

func qualifiedLaboratory(t *testing.T, e *Extension, caps map[string]any) Program {
	t.Helper()
	r := laboratoryReflex()
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	if err := qualifyIndependent(e, t.Context(), &r, caps); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReflexLibraryQualificationAndImmutableView(t *testing.T) {
	for _, condition := range []string{"qualified", "changed_source", "changed_checks", "changed_contract", "missing_registry"} {
		t.Run(condition, func(t *testing.T) {
			e := testLaboratory(t)
			r := qualifiedLaboratory(t, e, observationCapabilities("bash"))
			c := jevapi.Claim{Type: jevapi.ClaimNoul, Context: "Add current items and verify their completion."}
			cid := "c" + digest(c)[:16]
			if condition == "changed_source" {
				r.Observe += " "
			}
			if condition == "changed_checks" {
				r.Proof.Checks = nil
			}
			rid := "r" + digest(r)[:16]
			lib := library{Claims: map[string]claimRecord{cid: {Claim: c, Task: "source-task"}}, Reflexes: map[string]Reflex{rid: {Program: r, Claims: []string{cid}}}}
			if err := e.saveLibrary(lib); err != nil {
				t.Fatal(err)
			}
			next := New(Config{Directory: e.config.Directory})
			next.contracts = e.contracts
			if condition == "changed_contract" {
				suite := laboratorySuite()
				contract := suite.Contracts["lab"]
				contract.Version = "changed"
				suite.Contracts["lab"] = contract
				if err := testVerification(next).Register(suite); err != nil {
					t.Fatal(err)
				}
			}
			if condition == "missing_registry" {
				next.contracts = nil
			}
			if err := next.loadLibrary(); err != nil {
				t.Fatal(err)
			}
			loaded := next.snapshot()
			if loaded.Claims[cid].Claim.Context != c.Context || loaded.Claims[cid].Task != "source-task" {
				t.Fatal("lost Claim provenance")
			}
			if condition == "qualified" {
				if len(loaded.published()) != 1 || loaded.Reflexes[rid].program == nil {
					t.Fatal("qualified source did not reload")
				}
				loaded.Reflexes[rid].Proof.Contracts["lab"] = "altered"
				loaded.Reflexes[rid].Steps["add"] = StepDefinition{Count: 99}
				if !next.qualified(next.snapshot().Reflexes[rid].Program) {
					t.Fatal("query mutated published proof or manifest")
				}
				return
			}
			archived := loaded.Reflexes[rid]
			if len(loaded.published()) != 0 || !archived.Archived || archived.ArchiveReason == "" || digest(archived.Program) != digest(r) {
				t.Fatal("invalidated publication was not retained as an archive")
			}
			backups, err := filepath.Glob(filepath.Join(e.config.Directory, "library-backup-*.json"))
			if err != nil || len(backups) != 0 {
				t.Fatal("reload created a second library", err)
			}
			again := New(Config{Directory: e.config.Directory})
			if err := again.loadLibrary(); err != nil || digest(again.snapshot()) != digest(loaded) {
				t.Fatal("archive did not reload unchanged", err)
			}
		})
	}
}

func TestReflexPublicationRequiresQualificationAndOwnsItsSnapshot(t *testing.T) {
	e := testLaboratory(t)
	c := jevapi.Claim{Type: jevapi.ClaimNoul, Context: "Add items with current parameters."}
	cid := "c" + digest(c)[:16]
	e.library.Claims[cid] = claimRecord{Claim: c}
	p := &compilation{claims: map[string]jevapi.Claim{cid: c}, ids: []string{cid}, capabilities: observationCapabilities("bash")}
	r := laboratoryReflex()
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	if err := e.publishReflex(p, &r); err == nil {
		t.Fatal("unqualified draft published")
	}
	if err := qualifyIndependent(e, t.Context(), &r, p.capabilities); err != nil {
		t.Fatal(err)
	}
	if err := e.publishReflex(p, &r); err != nil {
		t.Fatal(err)
	}
	if len(e.snapshot().Reflexes) != 1 {
		t.Fatal("qualified source was not published")
	}
	r.Steps["add"] = StepDefinition{Count: 99}
	for _, published := range e.snapshot().Reflexes {
		if !e.qualified(published.Program) {
			t.Fatal("compiler retained mutable published manifest")
		}
		if published.arguments != nil {
			t.Fatal("publication retained compilation example arguments")
		}
	}
	lib := e.snapshot()
	lib.Reflexes = map[string]Reflex{"bad-id": {Program: r, Claims: []string{cid}}}
	if err := e.saveLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if err := e.loadLibrary(); err == nil {
		t.Fatal("tampered program identity accepted")
	}
}

func TestReflexMissingNativeEvidenceDoesNotPersistDraft(t *testing.T) {
	e := testLaboratory(t)
	r := laboratoryReflex()
	r.Steps["add"] = StepDefinition{Contract: "unavailable", Count: 1}
	before := digest(e.snapshot())
	err := e.qualify(t.Context(), &r, observationCapabilities("bash"))
	if err == nil || !strings.Contains(err.Error(), "contract unavailable") || digest(e.snapshot()) != before {
		t.Fatalf("missing evidence persisted a draft: %v", err)
	}
}

func TestReflexKnownFailureAndUnknownResolution(t *testing.T) {
	s := laboratorySuite()
	c := coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":"lab add actor"}`), Step: "add"}
	c, _ = prepareBinding(c)
	l := newEffectLedger()
	_, _, _ = l.reserve("task", c)
	l.complete("task", c, map[string]any{"is_error": true}, errors.New("response lost"))
	second := c
	second.Occurrence = 1
	if _, _, err := l.reserve("task", second); err == nil {
		t.Fatal("unknown outcome allowed another mutation")
	}
	read, _ := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":"lab status other"}`), Read: true})
	l.reconcile(s.Contracts, read, map[string]any{"data": map[string]any{"complete": true}})
	if _, _, err := l.reserve("task", second); err == nil {
		t.Fatal("unrelated status resolved an effect")
	}
	read.Arguments = json.RawMessage(`{"command":"lab status actor"}`)
	read, _ = prepareBinding(read)
	l.reconcile(s.Contracts, read, map[string]any{"call_id": "actual-read", "data": map[string]any{"complete": true}})
	if _, cached, err := l.reserve("task", c); err != nil || !cached {
		t.Fatal("resolved occurrence redispatched")
	}
	if _, cached, err := l.reserve("task", second); err != nil || cached {
		t.Fatal("resolved effect did not allow the next occurrence")
	}
	contract := s.Contracts["lab"]
	contract.Outcome = func(coretool.NativeCall, map[string]any) string { return "not_applied" }
	s.Contracts["lab"] = contract
	l.complete("task", second, map[string]any{"is_error": true}, errors.New("explicit rejection"))
	l.classifyOutcome("task", second, s.Contracts, map[string]any{"is_error": true}, "lab")
	if _, _, err := l.reserve("task", coretool.NativeCall{Step: "another"}); err != nil {
		t.Fatal("trusted definite rejection was treated as unknown")
	}
}

func TestReflexOutcomeUsesDeclaredContract(t *testing.T) {
	s := laboratorySuite()
	other := s.Contracts["lab"]
	other.ID = "other"
	other.Outcome = func(coretool.NativeCall, map[string]any) string { return "applied" }
	other.Resolve = func(coretool.NativeCall, coretool.NativeCall, map[string]any) bool { return true }
	s.Contracts[other.ID] = other
	l := newEffectLedger()
	c, _ := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":"lab add actor"}`), Step: "add"})
	_, _, _ = l.reserve("task", c)
	result := map[string]any{"is_error": false, "data": map[string]any{"status": 503}}
	l.complete("task", c, result, nil)
	l.classifyOutcome("task", c, s.Contracts, result, "lab")
	read, _ := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":"lab status wrong"}`), Read: true})
	l.reconcile(s.Contracts, read, map[string]any{"data": map[string]any{"complete": true}})
	next := c
	next.Occurrence = 1
	if _, _, err := l.reserve("task", next); err == nil {
		t.Fatal("another contract resolved the declared contract's uncertain effect")
	}
	if _, _, err := l.reserve("task", c, "other"); err == nil {
		t.Fatal("existing logical operation accepted a different native contract")
	}
}

func TestReflexOrdinaryReadRecoveryAndInputSteering(t *testing.T) {
	for _, changeActor := range []bool{false, true} {
		t.Run(fmt.Sprint(changeActor), func(t *testing.T) {
			effects := 0
			cmd := coretool.Command{Name: "lab", Run: func(_ context.Context, ex *coretool.Execution) (any, error) {
				if ex.Args[0] == "add" {
					effects++
					status := 200
					if effects == 1 {
						status = 503
					}
					fmt.Fprint(ex.Stdout, jsonText(map[string]any{"status": status}))
				} else {
					fmt.Fprint(ex.Stdout, jsonText(map[string]any{"status": 200, "complete": true, "count": effects, "receipt": "real-receipt"}))
				}
				return nil, nil
			}}
			client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
				if runtimeRequest(req) {
					return runtimeAnswers(req, "run")
				}
				return declarationAnswers(req, false)
			})
			e, cfg, _ := testInstallation(t, Config{Mode: "auto"}, client, cmd)
			if err := testVerification(e).Register(laboratorySuite()); err != nil {
				t.Fatal(err)
			}
			caps, err := e.capabilities(cfg)
			if err != nil {
				t.Fatal(err)
			}
			r := qualifiedLaboratory(t, e, caps)
			e.library.Reflexes["r"+digest(r)[:16]] = Reflex{Program: r}
			parameters := 0
			cfg.Provider = testProvider(func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
				if strings.HasPrefix(provider.MessageText(req.Messages[0]), "Supply only the CURRENT") {
					parameters++
					actor := "actor"
					if changeActor && parameters > 1 {
						actor = "changed"
					}
					return reply(provider.TextMessage("assistant", jsonText(map[string]any{"actor": actor, "query": parameters > 1, "count": 2}))), nil
				}
				return reply(provider.TextMessage("assistant", `{"claims":[]}`)), nil
			})
			ev := hooks.ContextEvent{SessionID: "recovery", TurnID: "live", Messages: []*aop.Message{provider.TextMessage("user", "Add two items for actor.")}}
			ctx := agent.ContextWithToolAgentConfig(t.Context(), cfg)
			first, err := e.beforeModel(ctx, ev)
			if err != nil || effects != 1 || !strings.Contains(jsonText(first), "effect_unknown") {
				t.Fatalf("first handoff: %v %d", err, effects)
			}
			ordinary := operation.ContextWithInvocation(ctx, operation.Invocation{SessionID: ev.SessionID, TurnID: ev.TurnID, Emitter: "agent", CallID: "supplement"})
			result, err := cfg.Tools.ExecuteTool(ordinary, "bash", `{"command":"lab add actor"}`)
			if effects != 1 || (err == nil && (result == nil || !result.IsError)) {
				t.Fatalf("ordinary supplementation performed a mutation: effects=%d error=%v result=%v", effects, err, result)
			}
			result, err = cfg.Tools.ExecuteTool(ordinary, "bash", `{"command":"lab status actor"}`)
			if err != nil || result.IsError {
				t.Fatalf("trusted supplementation read blocked: %v %v", err, result)
			}
			wrong, wrongErr := cfg.Tools.ExecuteTool(ordinary, "bash", `{"command":"lab status unrelated"}`)
			if wrongErr == nil && (wrong == nil || !wrong.IsError) {
				t.Fatal("ordinary read escaped the current task target contract")
			}
			ev.Messages = append(ev.Messages, provider.TextMessage("user", "Query capability is available; continue using the existing operation."))
			second, err := e.beforeModel(ctx, ev)
			if err != nil {
				t.Fatal(err)
			}
			if changeActor {
				if effects != 1 || !strings.Contains(jsonText(second), "effect_binding_conflict") {
					t.Fatal("steering changed an existing effect's binding")
				}
			} else if effects != 2 || !strings.Contains(jsonText(second), "REPORT:") {
				t.Fatalf("recovery repeated or lost effects: %d %s", effects, jsonText(second))
			}
		})
	}
}

func TestReflexEvidencePathsTraverseCurrentArrays(t *testing.T) {
	evidence := map[string]map[string]any{"current": {"data": map[string]any{"elements": []any{map[string]any{"text": "actual receipt"}}}}}
	for _, index := range []any{0, int64(0), float64(0), json.Number("0")} {
		value, err := resolveReport(map[string]any{"evidence": "current", "path": []any{"data", "elements", index, "text"}}, evidence)
		if err != nil || value != "actual receipt" {
			t.Fatalf("index=%v value=%v err=%v", index, value, err)
		}
	}
	for _, index := range []any{-1, 1, 0.5, "0", json.Number("1.5")} {
		if _, err := resolveReport(map[string]any{"evidence": "current", "path": []any{"data", "elements", index, "text"}}, evidence); err == nil {
			t.Fatalf("invalid index accepted: %v", index)
		}
	}
	for _, path := range [][]any{{"data", "elements", 0, "absent"}, {"data", "elements", 0, "text", "child"}} {
		if _, err := resolveReport(map[string]any{"evidence": "current", "path": path}, evidence); err == nil {
			t.Fatalf("unavailable field accepted: %v", path)
		}
	}
}

func TestReflexShellAndEvidenceBoundaries(t *testing.T) {
	for _, script := range []string{"lab add $ACTOR", "lab add $(whoami)", "lab add actor > file", "lab add actor; lab add actor", "lab add *", "lab add ~", "lab add {a,b}"} {
		c, err := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(jsonText(map[string]any{"command": script})), Argv: []string{"lab", "add", "forged"}})
		if err != nil || len(c.Argv) != 0 {
			t.Fatalf("opaque shell gained trusted argv: %s %v", script, c.Argv)
		}
		if _, err := laboratorySuite().Contracts.Access(c); err == nil {
			t.Fatal("opaque shell was admitted")
		}
	}
	a, _ := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":"lab add actor"}`)})
	b, _ := prepareBinding(coretool.NativeCall{Name: "bash", Arguments: json.RawMessage(`{"command":" lab   add 'actor' "}`)})
	if logicalBinding(a) != logicalBinding(b) {
		t.Fatal("cosmetic shell changes altered operation identity")
	}
	ref := map[string]any{"evidence": "foreign-call", "path": []any{"data", "receipt"}}
	if _, err := resolveReport(ref, map[string]map[string]any{}); err == nil {
		t.Fatal("foreign task evidence accepted")
	}
	ref["path"] = []any{"data", "missing"}
	if _, err := resolveReport(ref, map[string]map[string]any{"foreign-call": {"data": map[string]any{"receipt": "real"}}}); err == nil {
		t.Fatal("missing evidence field accepted")
	}
}

func TestReflexCompilerEffortAndCancellationOwnItsLifetime(t *testing.T) {
	e := testLaboratory(t)
	e.config.DeclarationEffort = "none"
	providerCalls := 0
	p := &compilation{job: declaration{cfg: agent.Config{Provider: testProvider(func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		providerCalls++
		if req.ReasoningEffort != "none" {
			t.Error("compiler lost configured reasoning effort")
		}
		if req.Timeout < 30*time.Minute {
			t.Error("compiler inherited a short foreground request deadline")
		}
		return reply(provider.TextMessage("assistant", "null")), nil
	})}}}
	c := e.newCompilerAgent(p)
	if c.worker.Cfg.MaxTurns != 0 {
		t.Fatal("compiler still has a turn-count cutoff")
	}
	var r *Program
	if err := c.generate(t.Context(), map[string]any{}, &r); err != nil || providerCalls != 1 {
		t.Fatal(err)
	}
}

func TestReflexReportChecksCurrentRequest(t *testing.T) {
	s := laboratorySuite()
	evidence := map[string]map[string]any{"read": {"data": map[string]any{"complete": true, "count": 1, "receipt": "real"}}}
	err := s.CheckReport(VerificationReport{Arguments: map[string]any{"count": 2}, Report: map[string]any{"count": 1, "receipt": "real"}, Evidence: evidence})
	if err == nil {
		t.Fatal("a real receipt from incomplete work satisfied the current request")
	}
}

func TestReflexPureSemanticCapabilityAndFreshArguments(t *testing.T) {
	s := VerificationSuite{ID: "semantic", Version: "1", Contracts: coretool.NativeContracts{}, CheckInput: func(input, args map[string]any) error {
		if args == nil || input["user"] != fmt.Sprint(args["mode"])+":"+fmt.Sprint(args["value"]) {
			return errors.New("arguments do not represent this request")
		}
		return nil
	}, CheckCall: func(VerificationCall) error { return errors.New("pure capability has no native calls") }, CheckReport: func(r VerificationReport) error {
		expected := fmt.Sprint(r.Arguments["value"])
		if r.Arguments["mode"] == "upper" {
			expected = strings.ToUpper(expected)
		}
		p, ok := r.Report.(map[string]any)
		if !ok || p["text"] != expected {
			return errors.New("wrong semantic result")
		}
		return nil
	}, Cases: func(map[string]any) []VerificationCase {
		cases := []VerificationCase{}
		for i, mode := range []string{"upper", "keep"} {
			value := "MiXeD text"
			expected := value
			if mode == "upper" {
				expected = strings.ToUpper(value)
			}
			cases = append(cases, VerificationCase{ID: fmt.Sprint(i), Input: map[string]any{"user": mode + ":" + value}, Arguments: map[string]any{"mode": mode, "value": value},
				Judge: func(claim jevapi.Claim) (*decision.Evaluation, error) {
					return &decision.Evaluation{Value: &decision.Evaluation_Choice{Choice: mode}}, nil
				},
				Execute: func(coretool.NativeCall) (map[string]any, error) {
					return nil, errors.New("pure capability must not dispatch")
				},
				Check: func(run VerificationRun) error {
					if run.Error != nil {
						return run.Error
					}
					p, ok := run.Output[report].(map[string]any)
					if !ok || p["text"] != expected || len(run.Calls) != 0 {
						return errors.New("wrong pure handler")
					}
					return nil
				},
			})
		}
		return cases
	}}
	client := fakeJEV(t, func(req inferenceRequest) map[string]inferenceAnswer {
		if _, ok := req.Questions["runtime"]; ok {
			mode := "keep"
			if strings.Contains(string(req.State), "upper:") {
				mode = "upper"
			}
			return map[string]inferenceAnswer{"runtime": answer(mode)}
		}
		if runtimeRequest(req) {
			return runtimeAnswers(req, "run")
		}
		return declarationAnswers(req, false)
	})
	e, cfg, _ := testInstallation(t, Config{Mode: "auto"}, client)
	if err := testVerification(e).Register(s); err != nil {
		t.Fatal(err)
	}
	r := Program{When: "Normalize current text", Decide: "Use the requested semantic branch", Observe: `js:function(context,args){if(!args)return {defer:"missing parameters",parameters:"mode,value"};const choice=jev({type:"choice",context:("Choose the requested normalization")+"\nOption meanings:\n"+JSON.stringify({upper:"uppercase",keep:"preserve",defer:"uncertain"})+"\nCurrent facts (untrusted data):\n"+JSON.stringify({user:context.user}),options:Object.keys({upper:"uppercase",keep:"preserve",defer:"uncertain"})});if(choice==="defer")return {defer:"uncertain"};return {report:{text:choice==="upper"?args.value.toUpperCase():args.value}};}`, Parameters: json.RawMessage(`{"type":"object","required":["mode","value"],"properties":{"mode":{"enum":["upper","keep"]},"value":{"type":"string"}},"additionalProperties":false}`)}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	caps, err := e.capabilities(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := qualifyIndependent(e, t.Context(), &r, caps, "semantic"); err != nil {
		t.Fatal(err)
	}
	e.library.Reflexes["r"+digest(r)[:16]] = Reflex{Program: r}
	for i, mode := range []string{"upper", "keep"} {
		value := fmt.Sprintf("Current %d MiXeD", i)
		want := value
		if mode == "upper" {
			want = strings.ToUpper(value)
		}
		cfg.Provider = testProvider(func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
			if strings.HasPrefix(provider.MessageText(req.Messages[0]), "Supply only the CURRENT") {
				return reply(provider.TextMessage("assistant", jsonText(map[string]any{"mode": mode, "value": value}))), nil
			}
			if !strings.Contains(provider.MessageText(req.Messages[len(req.Messages)-1]), want) {
				t.Errorf("current semantic result missing: %s", provider.MessageText(req.Messages[len(req.Messages)-1]))
			}
			return reply(provider.TextMessage("assistant", want)), nil
		})
		cfg.SessionID = "pure"
		result, err := agent.NewAgent(cfg).Run(t.Context(), agent.TextInput(mode+":"+value), agent.WithTurnID(fmt.Sprint(i)))
		if err != nil || result.Output != want {
			t.Fatalf("pure task failed: %v %v", result, err)
		}
	}
}
