package inner

import (
	"bytes"
	"fmt"
	"matrixone/pkg/compress"
	"matrixone/pkg/container/batch"
	"matrixone/pkg/container/block"
	"matrixone/pkg/container/vector"
	"matrixone/pkg/encoding"
	"matrixone/pkg/hash"
	"matrixone/pkg/intmap/fastmap"
	"matrixone/pkg/vm/mempool"
	"matrixone/pkg/vm/metadata"

	"matrixone/pkg/vm/engine"
	"matrixone/pkg/vm/process"

	"github.com/google/uuid"
)

func init() {
	ZeroBools = make([]bool, UnitLimit)
	OneUint64s = make([]uint64, UnitLimit)
	for i := range OneUint64s {
		OneUint64s[i] = 1
	}
}

func String(arg interface{}, buf *bytes.Buffer) {
	n := arg.(*Argument)
	buf.WriteString(fmt.Sprintf("%s ⨝ %s", n.R, n.S))
}

func Prepare(proc *process.Process, arg interface{}) error {
	n := arg.(*Argument)
	n.Ctr = Container{
		diffs:  make([]bool, UnitLimit),
		matchs: make([]int64, UnitLimit),
		hashs:  make([]uint64, UnitLimit),
		sels:   make([][]int64, UnitLimit),
		groups: make(map[uint64][]*hash.SetGroup),
		slots:  fastmap.Pool.Get().(*fastmap.Map),
	}
	ctr := &n.Ctr
	uuid, err := uuid.NewUUID()
	if err != nil {
		fastmap.Pool.Put(ctr.slots)
		return err
	}
	ctr.spill.id = fmt.Sprintf("%s.%v", proc.Id, uuid)
	return nil
}

func Call(proc *process.Process, arg interface{}) (bool, error) {
	n := arg.(*Argument)
	ctr := &n.Ctr
	for {
		switch ctr.state {
		case Build:
			ctr.spill.e = n.E
			if err := ctr.build(n.Attrs, proc); err != nil {
				ctr.clean(proc)
				return true, err
			}
			ctr.state = Probe
		case Probe:
			return ctr.probe(n.R, n.S, n.Attrs, proc)
		}
	}
}

// R ⨝ S - S is the smaller relation
func (ctr *Container) build(attrs []string, proc *process.Process) error {
	var err error

	reg := proc.Reg.Ws[1]
	for {
		v := <-reg.Ch
		if v == nil {
			reg.Ch = nil
			reg.Wg.Done()
			break
		}
		bat := v.(*batch.Batch)
		if bat == nil || bat.Attrs == nil {
			reg.Wg.Done()
			continue
		}
		if ctr.spill.attrs == nil {
			bat.Reorder(attrs)
			ctr.spill.attrs = make([]string, len(bat.Attrs))
			for i, attr := range bat.Attrs {
				ctr.spill.attrs[i] = attr
			}
			ctr.spill.cs = make([]uint64, len(bat.Attrs))
			ctr.spill.md = make([]metadata.Attribute, len(bat.Attrs))
			for i, attr := range bat.Attrs {
				vec, err := bat.GetVector(attr, proc)
				if err != nil {
					reg.Ch = nil
					reg.Wg.Done()
					bat.Clean(proc)
					return err
				}
				ctr.spill.md[i] = metadata.Attribute{
					Name: attr,
					Type: vec.Typ,
					Alg:  compress.Lz4,
				}
				ctr.spill.cs[i] = encoding.DecodeUint64(vec.Data[:mempool.CountSize])
			}
		} else {
			bat.Reorder(ctr.spill.attrs)
		}
		if err = bat.Prefetch(attrs, bat.Vecs[:len(attrs)], proc); err != nil {
			reg.Ch = nil
			reg.Wg.Done()
			bat.Clean(proc)
			return err
		}
		ctr.bats = append(ctr.bats, &block.Block{
			Bat:   bat,
			R:     ctr.spill.r,
			Cs:    ctr.spill.cs,
			Attrs: ctr.spill.attrs,
		})
		if len(bat.Sels) == 0 {
			if err = ctr.buildBatch(bat.Vecs[:len(attrs)], proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				return err
			}
		} else {
			if err = ctr.buildBatchSels(bat.Sels, bat.Vecs[:len(attrs)], proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				return err
			}
		}
		switch {
		case ctr.spilled:
			blk := ctr.bats[len(ctr.bats)-1]
			if err := blk.Bat.Prefetch(blk.Bat.Attrs, blk.Bat.Vecs, proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				return err
			}
			if err := ctr.spill.r.Write(blk.Bat); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				return err
			}
			blk.Seg = ctr.spill.r.Segments()[len(ctr.bats)-1]
			blk.Bat.Clean(proc)
			blk.Bat = nil
		case proc.Size() > proc.Lim.Size:
			if err := ctr.newSpill(proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				return err
			}
			for i, blk := range ctr.bats {
				if err := blk.Bat.Prefetch(blk.Bat.Attrs, blk.Bat.Vecs, proc); err != nil {
					reg.Ch = nil
					reg.Wg.Done()
					return err
				}
				if err := ctr.spill.r.Write(blk.Bat); err != nil {
					reg.Ch = nil
					reg.Wg.Done()
					return err
				}
				blk.R = ctr.spill.r
				blk.Seg = ctr.spill.r.Segments()[i]
				blk.Bat.Clean(proc)
				blk.Bat = nil
			}
			ctr.spilled = true
		}
		reg.Wg.Done()
	}
	return nil
}

func (ctr *Container) probe(rName, sName string, attrs []string, proc *process.Process) (bool, error) {
	for {
		reg := proc.Reg.Ws[0]
		v := <-reg.Ch
		if v == nil {
			reg.Ch = nil
			reg.Wg.Done()
			proc.Reg.Ax = nil
			ctr.clean(proc)
			return true, nil
		}
		bat := v.(*batch.Batch)
		if bat == nil || bat.Attrs == nil {
			reg.Wg.Done()
			continue
		}
		if len(ctr.groups) == 0 {
			reg.Ch = nil
			reg.Wg.Done()
			proc.Reg.Ax = nil
			bat.Clean(proc)
			ctr.clean(proc)
			return true, nil
		}
		if len(ctr.probeState.attrs) == 0 {
			bat.Reorder(attrs)
			ctr.probeState.attrs = make([]string, len(bat.Attrs))
			for i, attr := range bat.Attrs {
				ctr.probeState.attrs[i] = attr
			}
			ctr.probeState.md = make([]metadata.Attribute, len(bat.Attrs))
			for i, attr := range bat.Attrs {
				vec, err := bat.GetVector(attr, proc)
				if err != nil {
					reg.Ch = nil
					reg.Wg.Done()
					bat.Clean(proc)
					ctr.clean(proc)
					return true, err
				}
				ctr.probeState.md[i] = metadata.Attribute{
					Name: attr,
					Type: vec.Typ,
					Alg:  compress.Lz4,
				}
			}
			{
				ctr.attrs = make([]string, 0, len(bat.Attrs)+len(ctr.spill.attrs))
				for _, attr := range bat.Attrs {
					ctr.attrs = append(ctr.attrs, rName+"."+attr)
				}
				for _, attr := range ctr.spill.attrs {
					ctr.attrs = append(ctr.attrs, sName+"."+attr)
				}
			}
		} else {
			bat.Reorder(ctr.probeState.attrs)
		}
		if err := bat.Prefetch(bat.Attrs, bat.Vecs, proc); err != nil {
			reg.Ch = nil
			reg.Wg.Done()
			bat.Clean(proc)
			ctr.clean(proc)
			return true, err
		}
		ctr.probeState.bat = batch.New(true, ctr.attrs)
		{
			i := 0
			for _, m := range ctr.probeState.md {
				ctr.probeState.bat.Vecs[i] = vector.New(m.Type)
				i++
			}
			for _, m := range ctr.spill.md {
				ctr.probeState.bat.Vecs[i] = vector.New(m.Type)
				i++
			}
		}
		if len(bat.Sels) == 0 {
			if err := ctr.probeBatch(bat, bat.Vecs[:len(attrs)], proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				bat.Clean(proc)
				ctr.clean(proc)
				return true, err
			}
		} else {
			if err := ctr.probeBatchSels(bat.Sels, bat, bat.Vecs[:len(attrs)], proc); err != nil {
				reg.Ch = nil
				reg.Wg.Done()
				bat.Clean(proc)
				ctr.clean(proc)
				return true, err
			}
		}
		if ctr.probeState.bat.Vecs[0].Length() == 0 {
			reg.Wg.Done()
			bat.Clean(proc)
			continue
		}
		reg.Wg.Done()
		bat.Clean(proc)
		proc.Reg.Ax = ctr.probeState.bat
		ctr.probeState.bat = nil
		return false, nil
	}
}

func (ctr *Container) buildBatch(vecs []*vector.Vector, proc *process.Process) error {
	for i, j := 0, vecs[0].Length(); i < j; i += UnitLimit {
		length := j - i
		if length > UnitLimit {
			length = UnitLimit
		}
		if err := ctr.buildUnit(i, length, nil, vecs, proc); err != nil {
			return err
		}
	}
	return nil
}

func (ctr *Container) buildBatchSels(sels []int64, vecs []*vector.Vector, proc *process.Process) error {
	for i, j := 0, len(sels); i < j; i += UnitLimit {
		length := j - i
		if length > UnitLimit {
			length = UnitLimit
		}
		if err := ctr.buildUnit(0, length, sels[i:i+length], vecs, proc); err != nil {
			return err
		}
	}
	return nil
}

func (ctr *Container) buildUnit(start, count int, sels []int64,
	vecs []*vector.Vector, proc *process.Process) error {
	var err error

	{
		copy(ctr.hashs[:count], OneUint64s[:count])
		if len(sels) == 0 {
			ctr.fillHash(start, count, vecs)
		} else {
			ctr.fillHashSels(count, sels, vecs)
		}
	}
	copy(ctr.diffs[:count], ZeroBools[:count])
	for i, hs := range ctr.slots.Ks {
		for j, h := range hs {
			remaining := ctr.sels[ctr.slots.Vs[i][j]]
			if gs, ok := ctr.groups[h]; ok {
				for _, g := range gs {
					if remaining, err = g.Fill(remaining, ctr.matchs, vecs, ctr.bats, ctr.diffs, proc); err != nil {
						return err
					}
					copy(ctr.diffs[:len(remaining)], ZeroBools[:len(remaining)])
				}
			} else {
				ctr.groups[h] = make([]*hash.SetGroup, 0, 8)
			}
			for len(remaining) > 0 {
				g := hash.NewSetGroup(int64(len(ctr.bats)-1), int64(remaining[0]))
				ctr.groups[h] = append(ctr.groups[h], g)
				if remaining = remaining[1:]; len(remaining) > 0 {
					if remaining, err = g.Fill(remaining, ctr.matchs, vecs, ctr.bats, ctr.diffs, proc); err != nil {
						return err
					}
					copy(ctr.diffs[:len(remaining)], ZeroBools[:len(remaining)])
				}
			}
			ctr.sels[ctr.slots.Vs[i][j]] = ctr.sels[ctr.slots.Vs[i][j]][:0]
		}
	}
	ctr.slots.Reset()
	return nil
}

func (ctr *Container) probeBatch(bat *batch.Batch, vecs []*vector.Vector, proc *process.Process) error {
	for i, j := 0, vecs[0].Length(); i < j; i += UnitLimit {
		length := j - i
		if length > UnitLimit {
			length = UnitLimit
		}
		if err := ctr.probeUnit(i, length, nil, bat, vecs, proc); err != nil {
			return err
		}
	}
	return nil
}

func (ctr *Container) probeBatchSels(sels []int64, bat *batch.Batch, vecs []*vector.Vector, proc *process.Process) error {
	for i, j := 0, len(sels); i < j; i += UnitLimit {
		length := j - i
		if length > UnitLimit {
			length = UnitLimit
		}
		if err := ctr.probeUnit(0, length, sels[i:i+length], bat, vecs, proc); err != nil {
			return err
		}
	}
	return nil
}

func (ctr *Container) probeUnit(start, count int, sels []int64, bat *batch.Batch,
	vecs []*vector.Vector, proc *process.Process) error {
	var sel int64
	var err error

	{
		copy(ctr.hashs[:count], OneUint64s[:count])
		if len(sels) == 0 {
			ctr.fillHash(start, count, vecs)
		} else {
			ctr.fillHashSels(count, sels, vecs)
		}
	}
	copy(ctr.diffs[:count], ZeroBools[:count])
	for i, hs := range ctr.slots.Ks {
		for j, h := range hs {
			remaining := ctr.sels[ctr.slots.Vs[i][j]]
			if gs, ok := ctr.groups[h]; ok {
				for k := 0; k < len(gs); k++ {
					g := gs[k]
					if sel, remaining, err = g.Probe(remaining, ctr.matchs, vecs, ctr.bats, ctr.diffs, proc); err != nil {
						return err
					}
					if sel >= 0 {
						gs = append(gs[:k], gs[k+1:]...)
						k--
						if len(gs) == 0 {
							delete(ctr.groups, h)
						} else {
							ctr.groups[h] = gs
						}
						if err := ctr.product(sel, g, bat, proc); err != nil {
							return err
						}
					}
					copy(ctr.diffs[:len(remaining)], ZeroBools[:len(remaining)])
				}
			}
			ctr.sels[ctr.slots.Vs[i][j]] = ctr.sels[ctr.slots.Vs[i][j]][:0]
		}
	}
	ctr.slots.Reset()
	return nil
}

func (ctr *Container) product(sel int64, g *hash.SetGroup, bat *batch.Batch, proc *process.Process) error {
	{
		for i, vec := range bat.Vecs {
			if ctr.probeState.bat.Vecs[i].Data == nil {
				if err := ctr.probeState.bat.Vecs[i].UnionOne(vec, sel, proc); err != nil {
					return err
				}
				copy(ctr.probeState.bat.Vecs[i].Data[:mempool.CountSize], vec.Data[:mempool.CountSize])
			} else {
				if err := ctr.probeState.bat.Vecs[i].UnionOne(vec, sel, proc); err != nil {
					return err
				}
			}
		}
	}
	{
		k := len(bat.Vecs)
		bat, err := ctr.bats[g.Idx].GetBatch(proc)
		if err != nil {
			return err
		}
		defer func() {
			if len(ctr.bats[g.Idx].Seg.Id) > 0 && proc.Size() > proc.Lim.Size {
				bat.Clean(proc)
				ctr.bats[g.Idx].Bat = nil
			} else {
				ctr.bats[g.Idx].Bat = bat
			}
		}()
		if err := bat.Prefetch(bat.Attrs, bat.Vecs, proc); err != nil {
			return err
		}
		vecs := bat.Vecs
		for _, vec := range vecs {
			if ctr.probeState.bat.Vecs[k].Data == nil {
				if err := ctr.probeState.bat.Vecs[k].UnionOne(vec, g.Sel, proc); err != nil {
					return err
				}
				copy(ctr.probeState.bat.Vecs[k].Data[:mempool.CountSize], vec.Data[:mempool.CountSize])
			} else {
				if err := ctr.probeState.bat.Vecs[k].UnionOne(vec, g.Sel, proc); err != nil {
					return err
				}
			}
			k++
		}
	}
	return nil
}

func (ctr *Container) newSpill(proc *process.Process) error {
	var defs []engine.TableDef

	for _, attr := range ctr.spill.md {
		defs = append(defs, &engine.AttributeDef{attr})
	}
	if err := ctr.spill.e.Create(ctr.spill.id, defs, nil, nil); err != nil {
		return err
	}
	r, err := ctr.spill.e.Relation(ctr.spill.id)
	if err != nil {
		ctr.spill.e.Delete(ctr.spill.id)
		return err
	}
	ctr.spill.r = r
	return nil
}

func (ctr *Container) fillHash(start, count int, vecs []*vector.Vector) {
	ctr.hashs = ctr.hashs[:count]
	for _, vec := range vecs {
		hash.Rehash(count, ctr.hashs, vec.Window(start, start+count))
	}
	nextslot := 0
	for i, h := range ctr.hashs {
		slot, ok := ctr.slots.Get(h)
		if !ok {
			slot = nextslot
			ctr.slots.Set(h, slot)
			nextslot++
		}
		ctr.sels[slot] = append(ctr.sels[slot], int64(i+start))
	}
}

func (ctr *Container) fillHashSels(count int, sels []int64, vecs []*vector.Vector) {
	var cnt int64

	{
		for i, sel := range sels {
			if i == 0 || sel > cnt {
				cnt = sel
			}
		}
	}
	ctr.hashs = ctr.hashs[:cnt+1]
	for _, vec := range vecs {
		hash.RehashSels(sels[:count], ctr.hashs, vec)
	}
	nextslot := 0
	for i, h := range ctr.hashs {
		slot, ok := ctr.slots.Get(h)
		if !ok {
			slot = nextslot
			ctr.slots.Set(h, slot)
			nextslot++
		}
		ctr.sels[slot] = append(ctr.sels[slot], sels[i])
	}
}

func (ctr *Container) clean(proc *process.Process) {
	fastmap.Pool.Put(ctr.slots)
	if ctr.probeState.bat != nil {
		ctr.probeState.bat.Clean(proc)
		ctr.probeState.bat = nil
	}
	for _, blk := range ctr.bats {
		if blk.Bat != nil {
			blk.Bat.Clean(proc)
			blk.Bat = nil
		}
	}
	if ctr.spill.r != nil {
		ctr.spill.e.Delete(ctr.spill.id)
	}
	{
		for _, reg := range proc.Reg.Ws {
			if reg.Ch != nil {
				v := <-reg.Ch
				switch {
				case v == nil:
					reg.Ch = nil
					reg.Wg.Done()
				default:
					bat := v.(*batch.Batch)
					if bat == nil || bat.Attrs == nil {
						reg.Ch = nil
						reg.Wg.Done()
					} else {
						bat.Clean(proc)
						reg.Ch = nil
						reg.Wg.Done()
					}
				}
			}
		}
	}
}