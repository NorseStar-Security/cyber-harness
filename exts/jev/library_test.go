package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestLibraryRejectsDraftsAndInvalidReferences(t *testing.T) {
	e := testLaboratory(t)
	r := qualifiedLaboratory(t, e, observationCapabilities("bash"))
	c := jevapi.Claim{Type: jevapi.ClaimNoul, Context: "Inspect current native results."}
	cid := "c" + digest(c)[:16]
	rid := "r" + digest(r)[:16]
	lib := library{Claims: map[string]claimRecord{cid: {Claim: c}}, Reflexes: map[string]Reflex{rid: {Program: r, Claims: []string{cid}}}}
	for _, condition := range []string{"valid", "draft", "archived_draft", "missing_claim", "identity", "unknown_metadata", "unknown_proof", "candidate_collection", "schema_version"} {
		t.Run(condition, func(t *testing.T) {
			next := lib.clone()
			reflex := next.Reflexes[rid]
			switch condition {
			case "draft", "archived_draft":
				reflex.Proof = nil
				reflex.Archived = condition == "archived_draft"
				delete(next.Reflexes, rid)
				next.Reflexes["r"+digest(reflex.Program)[:16]] = reflex
			case "missing_claim":
				delete(next.Claims, cid)
			case "identity":
				reflex.Observe += " "
				next.Reflexes[rid] = reflex
			}
			data, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			if condition == "valid" {
				if _, err := decodeLibrary(data); err != nil {
					t.Fatal(err)
				}
				return
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			stored := document["reflexes"].(map[string]any)
			switch condition {
			case "unknown_metadata":
				stored[rid].(map[string]any)["blocker"] = "waiting"
			case "unknown_proof":
				stored[rid].(map[string]any)["verification"].(map[string]any)["format"] = "unsupported"
			case "candidate_collection":
				document["candidates"] = map[string]any{}
			case "schema_version":
				document["format"] = "unsupported"
			}
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeLibrary(data)
			if (err == nil) != (condition == "valid") {
				t.Fatalf("decode %s: %v", condition, err)
			}
		})
	}
}

func TestReflexMetadataDoesNotChangeProgramIdentity(t *testing.T) {
	e := testLaboratory(t)
	r := qualifiedLaboratory(t, e, observationCapabilities("bash"))
	identity, source := digest(r), reflexSourceHash(r)
	record := Reflex{Program: r, Claims: []string{"source"}, Contracts: map[string]string{"tool:bash": "current"}}
	record.Claims = append(record.Claims, "another")
	record.Archived, record.ArchiveReason = true, "Replaced"
	if digest(record.Program) != identity || reflexSourceHash(record.Program) != source || !e.qualified(record.Program) {
		t.Fatal("metadata invalidated the executable identity or qualification")
	}
}

func TestArchivedReflexCannotBypassPublicationCapacity(t *testing.T) {
	e := testLaboratory(t)
	r := qualifiedLaboratory(t, e, observationCapabilities("bash"))
	c := jevapi.Claim{Type: jevapi.ClaimNoul, Context: "Add current items."}
	cid := "c" + digest(c)[:16]
	e.library.Claims[cid] = claimRecord{Claim: c}
	plan := &compilation{claims: map[string]jevapi.Claim{cid: c}}
	var archived string
	for i := range maxReflexes + 1 {
		r.When = fmt.Sprintf("Add current items, scene %d", i)
		if err := qualifyIndependent(e, t.Context(), &r, observationCapabilities("bash")); err != nil {
			t.Fatal(err)
		}
		if err := e.publishReflex(plan, &r); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			archived = "r" + digest(r)[:16]
			if !e.retireReflex(t.Context(), archived, errors.New("retire")) {
				t.Fatal("retirement failed")
			}
		}
	}
	before := digest(e.snapshot())
	old := e.snapshot().Reflexes[archived].Program
	if err := e.publishReflex(plan, &old); err == nil || digest(e.snapshot()) != before {
		t.Fatal("reactivating an archive bypassed the published limit")
	}
	for id := range e.snapshot().published() {
		plan.job.repair = id
		break
	}
	if err := e.publishReflex(plan, &old); err != nil {
		t.Fatal(err)
	}
	lib := e.snapshot()
	if len(lib.published()) != maxReflexes || !lib.Reflexes[plan.job.repair].Archived || lib.Reflexes[archived].Archived {
		t.Fatal("replacement did not exchange one publication for one archive")
	}
}

func TestTypedReflexSnapshotPreservesDefinitionAndQualification(t *testing.T) {
	e := testLaboratory(t)
	r := qualifiedLaboratory(t, e, observationCapabilities("bash"))
	r.Proof.CoverageGaps = []string{"No recorded fault branch"}
	record := Reflex{Program: r, Archived: true, ArchiveReason: "Native contract changed"}
	definition := reflexDefinition("r"+digest(r)[:16], record)
	data, err := protojson.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ReflexDefinition
	if err := protojson.Unmarshal(data, &decoded); err != nil || !proto.Equal(definition, &decoded) {
		t.Fatal("typed definition did not round trip", err)
	}
	if definition.Qualification.SourceHash != r.Proof.SourceHash || definition.Qualification.Replayed != int64(r.Proof.Replayed) || definition.Qualification.CoverageGaps[0] != r.Proof.CoverageGaps[0] || definition.Steps["add"].CountArgument != "count" || definition.ParametersSchema.GetStructValue().AsMap()["type"] != "object" || !definition.Archived || definition.ArchiveReason != record.ArchiveReason {
		t.Fatal("typed definition lost qualification, manifest or archive facts")
	}
	definition.Qualification.Contracts["lab"] = "changed"
	if !e.qualified(record.Program) {
		t.Fatal("event snapshot mutated library qualification")
	}
}

func TestTypedReflexSnapshotPreservesBooleanParameterSchemas(t *testing.T) {
	for _, schema := range []string{"true", "false"} {
		definition := reflexDefinition("scene", Reflex{Program: Program{Parameters: json.RawMessage(schema)}})
		if definition.ParametersSchema == nil || definition.ParametersSchema.AsInterface() != (schema == "true") {
			t.Fatalf("snapshot discarded JSON Schema %s", schema)
		}
	}
}
