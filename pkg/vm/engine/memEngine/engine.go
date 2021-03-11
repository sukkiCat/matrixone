package memEngine

import (
	"matrixbase/pkg/encoding"
	"matrixbase/pkg/vm/engine"
	"matrixbase/pkg/vm/engine/memEngine/kv"
	"matrixbase/pkg/vm/engine/memEngine/meta"
	"matrixbase/pkg/vm/mempool"
	"matrixbase/pkg/vm/metadata"
	"matrixbase/pkg/vm/mmu/guest"
	"matrixbase/pkg/vm/mmu/host"
	"matrixbase/pkg/vm/process"
)

func New(db *kv.KV) *memEngine {
	return &memEngine{db, process.New(guest.New(1<<20, host.New(1<<20)), mempool.New(1<<32, 16))}
}

func (e *memEngine) Delete(name string) error {
	return e.db.Del(name)
}

func (e *memEngine) Create(name string, attrs []metadata.Attribute) error {
	var md meta.Metadata

	md.Name = name
	md.Attrs = attrs
	data, err := encoding.Encode(md)
	if err != nil {
		return err
	}
	return e.db.Set(name, data)
}

func (e *memEngine) Relations() []engine.Relation {
	return nil
}

func (e *memEngine) Relation(name string) (engine.Relation, error) {
	var md meta.Metadata

	data, err := e.db.Get(name, e.proc)
	if err != nil {
		return nil, err
	}
	defer e.proc.Free(data)
	if err := encoding.Decode(data[mempool.CountSize:], &md); err != nil {
		return nil, err
	}
	return &relation{name, e.db, md}, nil
}