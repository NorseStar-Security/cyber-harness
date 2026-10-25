package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
)

func decodeLibrary(data []byte) (library, error) {
	var lib library
	if len(data) > maxLibraryBytes {
		return lib, errors.New("JEV library capacity reached")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lib); err != nil {
		return lib, fmt.Errorf("invalid JEV library: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return lib, errors.New("extra JEV library data")
	}
	if lib.Claims == nil || lib.Reflexes == nil || len(lib.Claims) > maxClaims || len(lib.published()) > maxReflexes {
		return lib, errors.New("invalid JEV library collections or capacity")
	}
	for id, c := range lib.Claims {
		if c.Claim.Validate() != nil || id != "c"+digest(c.Claim)[:16] {
			return lib, fmt.Errorf("invalid Claim %s", id)
		}
	}
	for id, r := range lib.Reflexes {
		if id != "r"+digest(r.Program)[:16] {
			return lib, fmt.Errorf("invalid Reflex identity %s", id)
		}
		if r.Proof == nil {
			return lib, fmt.Errorf("Reflex %s has no qualification; compiler drafts do not belong in the library", id)
		}
		for _, claim := range r.Claims {
			if _, ok := lib.Claims[claim]; !ok {
				return lib, fmt.Errorf("Reflex %s references missing Claim %s", id, claim)
			}
		}
	}
	return lib, nil
}

func (lib library) published() map[string]Reflex {
	active := make(map[string]Reflex)
	for id, r := range lib.Reflexes {
		if !r.Archived {
			active[id] = r
		}
	}
	return active
}

func (lib library) archivedReflex(id, claim string) (string, Reflex, bool) {
	ids := make([]string, 0, len(lib.Reflexes))
	for key := range lib.Reflexes {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	for _, key := range ids {
		r := lib.Reflexes[key]
		if r.Archived && (key == id || id == "" && slices.Contains(r.Claims, claim)) {
			return key, r, true
		}
	}
	return "", Reflex{}, false
}
