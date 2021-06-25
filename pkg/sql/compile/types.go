package compile

import (
	"matrixone/pkg/container/types"
	"matrixone/pkg/sql/op"
	"matrixone/pkg/sql/op/relation"
	"matrixone/pkg/sql/tree"
	"matrixone/pkg/vm"
	"matrixone/pkg/vm/engine"
	"matrixone/pkg/vm/metadata"
	"matrixone/pkg/vm/process"
)

const (
	Normal = iota
	Merge
	Insert
)

type Source struct {
	ID   string
	DB   string
	N    metadata.Node
	Refs map[string]uint64
	Segs []*relation.Segment
}

type Scope struct {
	Magic int
	O     op.OP
	Data  *Source
	Ss    []*Scope
	Ins   vm.Instructions
	Proc  *process.Process
}

type Col struct {
	Typ  types.T
	Name string
}

type Exec struct {
	err  error
	cs   []*Col
	ss   []*Scope
	c    *compile
	e    engine.Engine
	stmt tree.Statement
}

type compile struct {
	db   string
	uid  string
	sql  string
	e    engine.Engine
	ns   metadata.Nodes
	proc *process.Process
}