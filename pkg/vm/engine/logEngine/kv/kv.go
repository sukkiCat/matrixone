package kv

import (
	"matrixbase/pkg/vm/engine/logEngine/kv/cache"
	"matrixbase/pkg/vm/engine/logEngine/kv/s3"
	"matrixbase/pkg/vm/process"

	aio "github.com/traetox/goaio"
)

func New(name string, re *s3.KV, kc *cache.Cache) (*KV, error) {
	return &KV{
		re:   re,
		kc:   kc,
		name: name,
	}, nil
}

func (a *KV) Close() error {
	return nil
}

func (a *KV) Del(k string) error {
	return a.re.Del(k)
}

func (a *KV) Add(k string, v []byte) error {
	return a.kc.Add(k, v)
}

func (a *KV) Set(k string, v []byte) error {
	return a.re.Set(k, v)
}

func (a *KV) Get(k string, proc *process.Process) ([]byte, *aio.AIO, aio.RequestId, error) {
	if v, ap, id, ok, err := a.kc.Get(k, proc); ok {
		return v, ap, id, err
	}
	v, ap, id, err := a.re.Get(k, proc)
	if err != nil {
		return nil, nil, 0, err
	}
	a.kc.Set(k, int64(len(v)))
	return v, ap, id, nil
}