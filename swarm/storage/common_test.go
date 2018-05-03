// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	colorable "github.com/mattn/go-colorable"
)

var (
	loglevel = flag.Int("loglevel", 3, "verbosity of logs")
)

func init() {
	flag.Parse()
	log.PrintOrigins(true)
	log.Root().SetHandler(log.LvlFilterHandler(log.Lvl(*loglevel), log.StreamHandler(colorable.NewColorableStderr(), log.TerminalFormat(true))))
}

type brokenLimitedReader struct {
	lr    io.Reader
	errAt int
	off   int
	size  int
}

func brokenLimitReader(data io.Reader, size int, errAt int) *brokenLimitedReader {
	return &brokenLimitedReader{
		lr:    data,
		errAt: errAt,
		size:  size,
	}
}

func mputChunks(store ChunkStore, processors int, n int, chunksize int64) (hs []Key) {
	return mput(store, processors, n, GenerateRandomChunk)
}
func mput(store ChunkStore, processors int, n int, f func(i int64) *Chunk) (hs []Key) {
	wg := sync.WaitGroup{}
	wg.Add(processors)
	c := make(chan *Chunk)
	for i := 0; i < processors; i++ {
		go func() {
			defer wg.Done()
			for chunk := range c {
				wg.Add(1)
				chunk := chunk
				go func() {
					defer wg.Done()

					store.Put(chunk)

					<-chunk.dbStoredC
				}()
			}
		}()
	}
	fa := f
	if _, ok := store.(*MemStore); ok {
		fa = func(i int64) *Chunk {
			chunk := f(i)
			chunk.markAsStored()
			return chunk
		}
	}
	for i := 0; i < n; i++ {
		chunk := fa(int64(i))
		hs = append(hs, chunk.Key)
		c <- chunk
	}
	close(c)
	wg.Wait()
	return hs
}

func mget(store ChunkStore, hs []Key, f func(h Key, chunk *Chunk) error) error {
	wg := sync.WaitGroup{}
	wg.Add(len(hs))
	errc := make(chan error)

	for _, k := range hs {
		go func(h Key) {
			defer wg.Done()
			chunk, err := store.Get(h)
			if err != nil {
				errc <- err
				return
			}
			if f != nil {
				err = f(h, chunk)
				if err != nil {
					errc <- err
					return
				}
			}
		}(k)
	}
	go func() {
		wg.Wait()
		close(errc)
	}()
	var err error
	select {
	case err = <-errc:
	case <-time.NewTimer(5 * time.Second).C:
		err = fmt.Errorf("timed out after 5 seconds")
	}
	return err
}

func testDataReader(l int) (r io.Reader) {
	return io.LimitReader(rand.Reader, int64(l))
}

func (r *brokenLimitedReader) Read(buf []byte) (int, error) {
	if r.off+len(buf) > r.errAt {
		return 0, fmt.Errorf("Broken reader")
	}
	r.off += len(buf)
	return r.lr.Read(buf)
}

func generateRandomData(l int) (r io.Reader, slice []byte) {
	slice = make([]byte, l)
	if _, err := rand.Read(slice); err != nil {
		panic("rand error")
	}
	r = io.LimitReader(bytes.NewReader(slice), int64(l))
	return
}

func testStoreRandom(m ChunkStore, processors int, n int, chunksize int64, t *testing.T) {
	hs := mputChunks(m, processors, n, chunksize)
	err := mget(m, hs, nil)
	if err != nil {
		t.Fatalf("testStore failed: %v", err)
	}
}

func testStoreCorrect(m ChunkStore, processors int, n int, chunksize int64, t *testing.T) {
	hs := mputChunks(m, processors, n, chunksize)
	f := func(h Key, chunk *Chunk) error {
		if !bytes.Equal(h, chunk.Key) {
			return fmt.Errorf("key does not match retrieved chunk Key")
		}
		hasher := MakeHashFunc(DefaultHash)()
		hasher.ResetWithLength(chunk.SData[:8])
		hasher.Write(chunk.SData[8:])
		exp := hasher.Sum(nil)
		if !bytes.Equal(h, exp) {
			return fmt.Errorf("key is not hash of chunk data")
		}
		return nil
	}
	err := mget(m, hs, f)
	if err != nil {
		t.Fatalf("testStore failed: %v", err)
	}
}

func benchmarkStorePut(store ChunkStore, processors int, n int, chunksize int64, b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mputChunks(store, processors, n, chunksize)
	}
}

func benchmarkStoreGet(store ChunkStore, processors int, n int, chunksize int64, b *testing.B) {
	hs := mputChunks(store, processors, n, chunksize)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := mget(store, hs, nil)
		if err != nil {
			b.Fatalf("mget failed: %v", err)
		}
	}
}

// MapChunkStore is a very simple ChunkStore implementation to store chunks in a map in memory.
type MapChunkStore struct {
	chunks map[string]*Chunk
	mu     sync.RWMutex
}

func NewMapChunkStore() *MapChunkStore {
	return &MapChunkStore{
		chunks: make(map[string]*Chunk),
	}
}

func (m *MapChunkStore) Put(_ context.Context, chunk Chunk) (func(context.Context) error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks[chunk.Key.Hex()] = chunk
	return nil, nil
}

func (m *MapChunkStore) Get(key Key) (Chunk, func(context.Context) (Chunk, error), error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	chunk := m.chunks[key.Hex()]
	if chunk == nil {
		return nil, nil, ErrChunkNotFound
	}
	return chunk, nil, nil
}

func (m *MapChunkStore) Close() {
}
