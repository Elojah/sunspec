package model

import (
	"fmt"
	"slices"
	"sync"
)

//go:generate go run -C ../tools/modelgen . -out ../../model/models.go

var (
	// mu guards definitions, which holds each definition Lookup built.
	mu          sync.Mutex
	definitions = map[uint16]*Definition{}
)

// Lookup returns the definition of model id among SunSpec's, or nil if SunSpec defines no such model. It builds it on
// first use, so that a program holds only the definitions it uses in memory, then shares it: never modify it. It is
// safe for concurrent use.
func Lookup(id uint16) *Definition {
	i, found := slices.BinarySearch(ids[:], id)
	if !found {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	d, ok := definitions[id]
	if !ok {
		r := reader{s: encoded[offsets[i]:offsets[i+1]]}
		decoded := r.definition()
		if r.err != nil {
			// tools/modelgen encoded it, from a definition Parse checked: the generated file is corrupt.
			panic(fmt.Sprintf("model: generated definition of model %d: %v", id, r.err))
		}
		d = &decoded
		definitions[id] = d
	}

	return d
}

// IDs returns the IDs of SunSpec's models, in order.
func IDs() []uint16 {
	return slices.Clone(ids[:])
}
