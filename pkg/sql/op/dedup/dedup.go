package dedup

import (
	"fmt"
	"matrixone/pkg/container/types"
	"matrixone/pkg/sql/colexec/extend"
	"matrixone/pkg/sql/op"
)

func New(prev op.OP, gs []*extend.Attribute) *Dedup {
	attrs := make(map[string]types.Type)
	{
		for _, g := range gs {
			attrs[g.Name] = g.Type.ToType()
		}
	}
	return &Dedup{
		Gs:    gs,
		Prev:  prev,
		Attrs: attrs,
	}
}

func (n *Dedup) String() string {
	r := fmt.Sprintf("%s -> δ([", n.Prev)
	for i, g := range n.Gs {
		switch i {
		case 0:
			r += fmt.Sprintf("%s", g.Name)
		default:
			r += fmt.Sprintf(", %s", g.Name)
		}
	}
	r += fmt.Sprintf("])")
	return r
}

func (n *Dedup) Name() string {
	return n.ID
}

func (n *Dedup) Rename(name string) {
	n.ID = name
}

func (n *Dedup) Columns() []string {
	return n.Prev.Columns()
}

func (n *Dedup) Attribute() map[string]types.Type {
	return n.Attrs
}