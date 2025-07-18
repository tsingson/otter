// Copyright (c) 2023 Alexey Mayshev and contributors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otter

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/maypok86/otter/v2/internal/generated/node"
	"github.com/maypok86/otter/v2/internal/xruntime"
	"github.com/maypok86/otter/v2/stats"
)

func getRandomSize(t *testing.T) int {
	t.Helper()

	const (
		minSize = 10
		maxSize = 1000
	)

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	return r.Intn(maxSize-minSize) + minSize
}

func TestComputeOp_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   ComputeOp
		want string
	}{
		{
			op:   CancelOp,
			want: "CancelOp",
		},
		{
			op:   WriteOp,
			want: "WriteOp",
		},
		{
			op:   InvalidateOp,
			want: "InvalidateOp",
		},
		{
			op:   -1,
			want: "<unknown otter.ComputeOp>",
		},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, tt.op.String())
	}
}

func TestMust(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		Must(&Options[int, int]{
			MaximumSize: -1,
		})
	})

	require.NotPanics(t, func() {
		Must[int, int](nil)
	})
}

func TestCache_Unbounded(t *testing.T) {
	t.Parallel()

	statsCounter := stats.NewCounter()
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	size := getRandomSize(t)
	done := make(chan struct{})
	count := 0
	c := Must[int, int](&Options[int, int]{
		StatsRecorder: statsCounter,
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			count++
			if count == size {
				done <- struct{}{}
			}
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}
	for i := size; i < 2*size; i++ {
		if c.has(i) {
			t.Fatalf("the key must not exist: %d", i)
		}
	}

	replaced := size / 2
	for i := 0; i < replaced; i++ {
		c.Set(i, i)
	}
	for i := replaced; i < size; i++ {
		c.Invalidate(i)
	}
	c.CleanUp()
	require.Equal(t, uint64(math.MaxUint64), c.GetMaximum())

	<-done
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseInvalidation] != size-replaced {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size-replaced, m[CauseInvalidation])
	}
	if m[CauseReplacement] != replaced {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", replaced, m[CauseReplacement])
	}
	if hitRatio := statsCounter.Snapshot().HitRatio(); hitRatio != 0.5 {
		t.Fatalf("not valid hit ratio. expected %.2f, but got %.2f", 0.5, hitRatio)
	}
}

func TestCache_PinnedWeight(t *testing.T) {
	t.Parallel()

	size := 10
	pinned := 4
	m := make(map[DeletionCause]int)
	mutex := sync.Mutex{}
	fs := &fakeSource{}
	var wg sync.WaitGroup
	c := Must[int, int](&Options[int, int]{
		MaximumWeight: uint64(size),
		Weigher: func(key int, value int) uint32 {
			if key == pinned {
				return 0
			}
			return 1
		},
		Clock:            fs,
		ExpiryCalculator: ExpiryWriting[int, int](2 * time.Second),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	wg.Add(size - 1)
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}
	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.has(i)
	}
	for i := size; i < 2*size; i++ {
		c.Set(i, i)
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
		c.has(i)
	}
	if !c.has(pinned) {
		t.Fatalf("the key must exist: %d", pinned)
	}

	wg.Wait()
	wg.Add(size + 1)
	fs.Sleep(4 * time.Second)
	wg.Wait()

	if c.has(pinned) {
		t.Fatalf("the key must not exist: %d", pinned)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseOverflow] != size-1 {
		t.Fatalf("cache was supposed to evict %d, but evicted %d entries", size-1, m[CauseOverflow])
	}
	if m[CauseExpiration] != size+1 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size+1, m[CauseExpiration])
	}
}

func TestCache_SetWithWeight(t *testing.T) {
	t.Parallel()

	statsCounter := stats.NewCounter()
	size := uint64(10)
	c := Must[uint32, int](&Options[uint32, int]{
		MaximumWeight:   size,
		InitialCapacity: 10000,
		Weigher: func(key uint32, value int) uint32 {
			return key
		},
		StatsRecorder: statsCounter,
	})
	c.cache.evictionPolicy.rand = func() uint32 {
		return 1
	}

	goodWeight1 := 1
	goodWeight2 := 2
	badWeight := 8

	c.Set(uint32(goodWeight1), 1)
	c.Set(uint32(goodWeight2), 1)
	c.Set(uint32(badWeight), 1)
	c.CleanUp()
	if !c.has(uint32(goodWeight1)) {
		t.Fatalf("the key must exist: %d", goodWeight1)
	}
	if !c.has(uint32(goodWeight2)) {
		t.Fatalf("the key must exist: %d", goodWeight2)
	}
	if c.has(uint32(badWeight)) {
		t.Fatalf("the key must not exist: %d", badWeight)
	}
}

func TestCache_All(t *testing.T) {
	t.Parallel()

	size := 10
	expiresAfter := time.Hour
	c := Must[int, int](&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryWriting[int, int](expiresAfter),
	})

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})

	c.Set(1, 1)
	c.cache.hashmap.Compute(2, func(n node.Node[int, int]) node.Node[int, int] {
		return nm.Create(2, 2, 1, 1, 1)
	})
	c.Set(3, 3)
	aliveNodes := 2
	iters := 0
	for key, value := range c.All() {
		if key != value {
			t.Fatalf("got unexpected key/value for iteration %d: %d/%d", iters, key, value)
			break
		}
		iters++
	}
	if iters != aliveNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
	i := 0
	foundKey := -1
	foundValue := -1
	for key, value := range c.All() {
		if i == 1 {
			foundKey = key
			foundValue = value
			break
		}
		i++
	}
	require.Contains(t, []int{1, 3}, foundKey)
	require.Contains(t, []int{1, 3}, foundValue)
}

func TestCache_Keys(t *testing.T) {
	t.Parallel()

	size := 10
	expiresAfter := time.Hour
	c := Must[int, int](&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryWriting[int, int](expiresAfter),
	})

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})

	c.Set(1, 1)
	c.cache.hashmap.Compute(2, func(n node.Node[int, int]) node.Node[int, int] {
		return nm.Create(2, 2, 1, 1, 1)
	})
	c.Set(3, 3)
	aliveNodes := 2
	iters := 0
	for key := range c.Keys() {
		if key != 1 && key != 3 {
			t.Fatalf("got unexpected key for iteration %d: %d", iters, key)
			break
		}
		iters++
	}
	if iters != aliveNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
	i := 0
	found := -1
	for key := range c.Keys() {
		if i == 1 {
			found = key
			break
		}
		i++
	}
	require.Contains(t, []int{1, 3}, found)
}

func TestCache_Values(t *testing.T) {
	t.Parallel()

	size := 10
	expiresAfter := time.Hour
	c := Must[int, int](&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryWriting[int, int](expiresAfter),
	})

	nm := node.NewManager[int, int](node.Config{
		WithExpiration: true,
		WithWeight:     true,
	})

	c.Set(1, 2)
	c.cache.hashmap.Compute(2, func(n node.Node[int, int]) node.Node[int, int] {
		return nm.Create(2, 3, 1, 1, 1)
	})
	c.Set(3, 4)
	aliveNodes := 2
	iters := 0
	for value := range c.Values() {
		if value != 2 && value != 4 {
			t.Fatalf("got unexpected value for iteration %d: %d", iters, value)
			break
		}
		iters++
	}
	if iters != aliveNodes {
		t.Fatalf("got unexpected number of iterations: %d", iters)
	}
	i := 0
	found := -1
	for value := range c.Values() {
		if i == 1 {
			found = value
			break
		}
		i++
	}
	require.Contains(t, []int{2, 4}, found)
}

func gcHelper(t *testing.T) *atomic.Bool {
	t.Helper()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
	})

	var cleaned atomic.Bool
	runtime.AddCleanup(c.cache, func(c *atomic.Bool) {
		c.Store(true)
	}, &cleaned)

	for i := 0; i < size; i++ {
		c.Set(i, i)
		c.has(i)
	}

	return &cleaned
}

func TestCache_GC(t *testing.T) {
	t.Parallel()

	cleaned := gcHelper(t)

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	if !cleaned.Load() {
		t.Fatal("cache should be collected")
	}
}

func TestCache_CleanUp(t *testing.T) {
	t.Parallel()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		ExpiryCalculator: ExpiryCreating[int, int](2 * time.Second),
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	for i := 0; i < size; i++ {
		c.has(i)
		c.has(i)
	}

	if l := c.cache.readBuffer.Len(); l == 0 {
		t.Fatalf("stripedBufferLen = %d, want > %d", l, 0)
	}
	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != size {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, size)
	}
	if l := c.cache.writeBuffer.Size(); l != 0 {
		t.Fatalf("writeBufferLen = %d, want = %d", l, 0)
	}
	if l := c.cache.readBuffer.Len(); l != 0 {
		t.Fatalf("stripedBufferLen = %d, want = %d", l, 0)
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	t.Parallel()

	size := 10
	c := Must(&Options[int, int]{
		MaximumSize: size,
		Executor: func(fn func()) {
			fn()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	if cacheSize := c.EstimatedSize(); cacheSize != size {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, size)
	}

	c.InvalidateAll()

	if cacheSize := c.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, 0)
	}
}

func TestCache_Set(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	done := make(chan struct{})
	count := 0
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](time.Minute),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			count++
			m[e.Cause]++
			if count == size {
				done <- struct{}{}
			}
			mutex.Unlock()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	// update
	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	parallelism := xruntime.Parallelism()
	var (
		wg  sync.WaitGroup
		err error
	)
	for i := 0; i < int(parallelism); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			for a := 0; a < 10000; a++ {
				k := r.Int() % size
				val, ok := c.GetIfPresent(k)
				if !ok {
					err = fmt.Errorf("expected %d but got nil", k)
					break
				}
				if val != k {
					err = fmt.Errorf("expected %d but got %d", k, val)
					break
				}
			}
		}()
	}
	wg.Wait()

	if err != nil {
		t.Fatalf("not found key: %v", err)
	}
	ratio := statsCounter.Snapshot().HitRatio()
	if ratio != 1.0 {
		t.Fatalf("cache hit ratio should be 1.0, but got %v", ratio)
	}

	<-done
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[CauseReplacement] != size {
		t.Fatalf("cache was supposed to replace %d, but replaced %d entries", size, m[CauseReplacement])
	}
}

func TestCache_SetIfAbsent(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	statsCounter := stats.NewCounter()
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		StatsRecorder:    statsCounter,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
	})

	for i := 0; i < size; i++ {
		if _, ok := c.SetIfAbsent(i, i); !ok {
			t.Fatalf("set was dropped. key: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		if _, ok := c.SetIfAbsent(i, i); ok {
			t.Fatalf("set wasn't dropped. key: %d", i)
		}
	}

	c.InvalidateAll()

	if hitRatio := statsCounter.Snapshot().HitRatio(); hitRatio != 1.0 {
		t.Fatalf("hit rate should be 100%%. Hite rate: %.2f", hitRatio*100)
	}
}

func TestCache_ComputeIfAbsent(t *testing.T) {
	t.Parallel()

	t.Run("functionCalledOnce", func(t *testing.T) {
		t.Parallel()

		const iters = 100
		c := Must(&Options[int, int]{})
		for i := 0; i < iters; i++ {
			actualValue, ok := c.ComputeIfAbsent(i, func() (newValue int, cancel bool) {
				newValue, i = i, i+1
				return newValue, false
			})
			require.True(t, ok)
			require.Equal(t, i-1, actualValue)
		}
		require.Equal(t, iters/2, c.EstimatedSize())
		for k, v := range c.All() {
			require.Equal(t, k, v)
		}
	})
	t.Run("general", func(t *testing.T) {
		t.Parallel()

		const entries = 1000
		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[int, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				deletions++
			},
		})
		for i := 0; i < entries; i++ {
			v, ok := c.ComputeIfAbsent(i, func() (newValue int, cancel bool) {
				return i, true
			})
			require.False(t, ok)
			require.Equal(t, 0, v)
		}
		require.Equal(t, 0, c.EstimatedSize())

		for i := 0; i < entries; i++ {
			v, ok := c.ComputeIfAbsent(i, func() (newValue int, cancel bool) {
				return i, false
			})
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		for i := 0; i < entries; i++ {
			v, ok := c.ComputeIfAbsent(i, func() (newValue int, cancel bool) {
				return i + 1, false
			})
			require.True(t, ok)
			require.Equal(t, i, v)
		}
		snapshot := counter.Snapshot()
		require.Equal(t, uint64(entries), snapshot.Hits)
		require.Equal(t, uint64(2*entries), snapshot.Misses)
		require.Equal(t, uint64(0), deletions)
	})
	t.Run("failedRead", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[int, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				deletions++
			},
		})
		key := 15

		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start
			v, ok := c.ComputeIfAbsent(key, func() (newValue int, cancel bool) {
				panic("incorrect call")
			})
			require.True(t, ok)
			require.Equal(t, key+1, v)
		}()

		v, ok := c.Compute(key, func(oldValue int, found bool) (newValue int, op ComputeOp) {
			start <- struct{}{}
			return key + 1, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, key+1, v)

		wg.Wait()

		snapshot := counter.Snapshot()
		require.Contains(t, []uint64{0, 1}, snapshot.Hits)
		require.Contains(t, []uint64{1, 2}, snapshot.Misses)
		require.Equal(t, uint64(0), deletions)
	})
}

func TestCache_ComputeIfPresent(t *testing.T) {
	t.Parallel()

	t.Run("general", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[string, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[string, int]) {
				deletions++
			},
		})

		// Store a new value.
		v, ok := c.Compute("foobar", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 0, oldValue)
			require.False(t, found)

			return 42, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, 42, v)

		// Update an existing value.
		v, ok = c.ComputeIfPresent("foobar", func(oldValue int) (newValue int, op ComputeOp) {
			require.Equal(t, 42, oldValue)

			return oldValue + 42, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, 84, v)

		// noop
		v, ok = c.ComputeIfPresent("foobar", func(oldValue int) (newValue int, op ComputeOp) {
			require.Equal(t, 84, oldValue)

			return 1, CancelOp
		})
		require.True(t, ok)
		require.Equal(t, 84, v)

		// noop
		v, ok = c.ComputeIfPresent("fizz", func(oldValue int) (newValue int, op ComputeOp) {
			panic("incorrect call")
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		// Delete an existing value.
		v, ok = c.ComputeIfPresent("foobar", func(oldValue int) (newValue int, op ComputeOp) {
			require.Equal(t, 84, oldValue)

			return 57, InvalidateOp
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		snapshot := counter.Snapshot()
		require.Equal(t, uint64(3), snapshot.Hits)
		require.Equal(t, uint64(2), snapshot.Misses)
		require.Equal(t, uint64(2), deletions)
	})
	t.Run("failedRead", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[int, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[int, int]) {
				deletions++
			},
		})
		key := 15

		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start
			v, ok := c.ComputeIfPresent(key, func(oldValue int) (newValue int, op ComputeOp) {
				panic("incorrect call")
			})
			require.False(t, ok)
			require.Equal(t, 0, v)
		}()

		c.Set(key, key)

		v, ok := c.Compute(key, func(oldValue int, found bool) (newValue int, op ComputeOp) {
			start <- struct{}{}
			return 0, InvalidateOp
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		wg.Wait()

		snapshot := counter.Snapshot()
		require.Contains(t, []uint64{1, 2}, snapshot.Hits)
		require.Contains(t, []uint64{0, 1}, snapshot.Misses)
		require.Equal(t, uint64(1), deletions)
	})
}

func TestCache_Compute(t *testing.T) {
	t.Parallel()

	t.Run("general", func(t *testing.T) {
		t.Parallel()

		counter := stats.NewCounter()
		deletions := uint64(0)
		c := Must(&Options[string, int]{
			StatsRecorder: counter,
			OnAtomicDeletion: func(e DeletionEvent[string, int]) {
				deletions++
			},
		})

		// Store a new value.
		v, ok := c.Compute("foobar", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 0, oldValue)
			require.False(t, found)

			return 42, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, 42, v)

		// Update an existing value.
		v, ok = c.Compute("foobar", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 42, oldValue)
			require.True(t, found)

			return oldValue + 42, WriteOp
		})
		require.True(t, ok)
		require.Equal(t, 84, v)

		// noop
		v, ok = c.Compute("foobar", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 84, oldValue)
			require.True(t, found)

			return 1, CancelOp
		})
		require.True(t, ok)
		require.Equal(t, 84, v)

		// Delete an existing value.
		v, ok = c.Compute("foobar", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 84, oldValue)
			require.True(t, found)

			return 0, InvalidateOp
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		// Try to delete a non-existing value. Notice different key.
		v, ok = c.Compute("barbaz", func(oldValue int, found bool) (newValue int, op ComputeOp) {
			require.Equal(t, 0, oldValue)
			require.False(t, found)

			// We're returning a non-zero value, but the cache should ignore it.
			return 42, InvalidateOp
		})
		require.False(t, ok)
		require.Equal(t, 0, v)

		snapshot := counter.Snapshot()
		require.Equal(t, uint64(3), snapshot.Hits)
		require.Equal(t, uint64(2), snapshot.Misses)
		require.Equal(t, uint64(2), deletions)
	})
	t.Run("panic", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		require.Panics(t, func() {
			c.Compute(0, func(oldValue int, found bool) (newValue int, op ComputeOp) {
				panic("olololololo")
			})
		})
		_, ok := c.GetIfPresent(0)
		require.False(t, ok)
	})
	t.Run("incorrectComputeOp", func(t *testing.T) {
		t.Parallel()

		c := Must(&Options[int, int]{})

		require.Panics(t, func() {
			c.Compute(0, func(oldValue int, found bool) (newValue int, op ComputeOp) {
				return -100, -1
			})
		})
		_, ok := c.GetIfPresent(0)
		require.False(t, ok)
	})
}

func TestCache_SetWithExpiresAt(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	wg.Add(size)
	c := Must(&Options[int, int]{
		InitialCapacity:  size,
		StatsRecorder:    statsCounter,
		Clock:            fs,
		ExpiryCalculator: ExpiryCreating[int, int](time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	fs.Sleep(time.Hour + 2*time.Second)
	for i := 0; i < size; i++ {
		if c.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, 0)
	}

	wg.Wait()
	mutex.Lock()
	if e := m[CauseExpiration]; len(m) != 1 || e != size {
		mutex.Unlock()
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, e)
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
	mutex.Unlock()

	m = make(map[DeletionCause]int)
	statsCounter = stats.NewCounter()
	if size%2 == 1 {
		size++
	}
	fs = &fakeSource{}
	wg.Add(size + size/2)
	cc := Must(&Options[int, int]{
		MaximumSize:   size,
		StatsRecorder: statsCounter,
		Clock:         fs,
		ExpiryCalculator: ExpiryWritingFunc(func(entry Entry[int, int]) time.Duration {
			if entry.Key%2 == 0 {
				return time.Hour
			}
			return 4 * time.Hour
		}),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for i := 0; i < size; i++ {
		cc.Set(i, i)
	}

	fs.Sleep(time.Hour + time.Second)

	if c.EstimatedSize() != size%2 {
		t.Fatalf("half of the keys must be expired. wantedCurrentSize %d, got %d", size%2, c.EstimatedSize())
	}

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		cc.Set(i, i)
	}

	fs.Sleep(3*time.Hour + time.Second)

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			continue
		}
		if !cc.has(i) {
			t.Fatalf("key should not be expired: %d", i)
		}
	}

	fs.Sleep(time.Hour + time.Second)

	for i := 0; i < size; i++ {
		if cc.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	cc.CleanUp()

	if cacheSize := cc.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("c.EstimatedSize() = %d, want = %d", cacheSize, 0)
	}
	if misses := statsCounter.Snapshot().Misses; misses != uint64(size) {
		t.Fatalf("c.Stats().Misses = %d, want = %d", misses, size)
	}
	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseExpiration] != size && m[CauseReplacement] != size/2 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[CauseExpiration])
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
}

func TestCache_SetWithExpiresAfterAccessing(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var (
		mutex sync.Mutex
		wg    sync.WaitGroup
	)
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	fs := &fakeSource{}
	wg.Add(size + size/2)
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		StatsRecorder:    statsCounter,
		Clock:            fs,
		ExpiryCalculator: ExpiryAccessing[int, int](2 * time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})
	c.cache.evictionPolicy.rand = func() uint32 {
		return 1
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	c.CleanUp()
	fs.Sleep(2*time.Hour + 1*time.Second)
	for i := 0; i < size; i++ {
		if c.has(i) {
			t.Fatalf("key should be expired: %d", i)
		}
	}

	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize != 0 {
		t.Fatalf("cacheSize = %d, want = %d", cacheSize, 0)
	}

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	fs.Sleep(time.Hour)

	for i := 0; i < size; i++ {
		if i%2 == 0 {
			if !c.has(i) {
				t.Fatalf("key should be expired: %d", i)
			}
		} else {
			c.Set(i, i)
		}
	}

	fs.Sleep(2 * time.Hour)
	c.CleanUp()

	if cacheSize := c.EstimatedSize(); cacheSize == 0 {
		t.Fatal("cacheSize should be positive")
	}
	if misses := statsCounter.Snapshot().Misses; misses != uint64(size) {
		t.Fatalf("c.Stats().Misses = %d, want = %d", misses, size)
	}
	wg.Wait()
	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 2 || m[CauseExpiration] != size && m[CauseReplacement] != size/2 {
		t.Fatalf("cache was supposed to expire %d, but expired %d entries", size, m[CauseExpiration])
	}
	if statsCounter.Snapshot().Evictions != uint64(m[CauseExpiration]) {
		mutex.Unlock()
		t.Fatalf(
			"Eviction statistics are not collected for expiration. Evictions: %d, expired entries: %d",
			statsCounter.Snapshot().Evictions,
			m[CauseExpiration],
		)
	}
}

func TestCache_Invalidate(t *testing.T) {
	t.Parallel()

	size := getRandomSize(t)
	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	var wg sync.WaitGroup
	wg.Add(size)
	c := Must(&Options[int, int]{
		MaximumSize:      size,
		InitialCapacity:  size,
		ExpiryCalculator: ExpiryWriting[int, int](time.Hour),
		OnDeletion: func(e DeletionEvent[int, int]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
			wg.Done()
		},
	})

	for i := 0; i < size; i++ {
		c.Set(i, i)
	}

	for i := 0; i < size; i++ {
		if !c.has(i) {
			t.Fatalf("the key must exist: %d", i)
		}
	}

	for i := 0; i < size; i++ {
		c.Invalidate(i)
	}

	for i := 0; i < size; i++ {
		if c.has(i) {
			t.Fatalf("the key must not exist: %d", i)
		}
	}

	c.CleanUp()
	wg.Wait()

	mutex.Lock()
	defer mutex.Unlock()
	if len(m) != 1 || m[CauseInvalidation] != size {
		t.Fatalf("cache was supposed to delete %d, but deleted %d entries", size, m[CauseInvalidation])
	}
}

func TestCache_ConcurrentInvalidateAll(t *testing.T) {
	t.Parallel()

	c := Must(&Options[string, string]{
		MaximumSize:      1000,
		ExpiryCalculator: ExpiryWriting[string, string](time.Hour),
	})

	done := make(chan struct{})
	go func() {
		const (
			goroutines = 10
			iterations = 5
		)

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				for j := 0; j < iterations; j++ {
					c.InvalidateAll()
				}
				wg.Done()
			}()
		}

		wg.Wait()
		done <- struct{}{}
	}()

	<-done
}

func TestCache_IsWeighted(t *testing.T) {
	t.Parallel()

	cache := Must(&Options[int, int]{
		MaximumSize: 1000,
	})

	require.False(t, cache.IsWeighted())

	cache = Must(&Options[int, int]{
		MaximumWeight: 1000,
		Weigher: func(key int, value int) uint32 {
			return uint32(value)
		},
	})

	require.True(t, cache.IsWeighted())
}

func TestCache_IsRecordingStats(t *testing.T) {
	t.Parallel()

	cache := Must(&Options[int, int]{
		StatsRecorder: stats.NewCounter(),
	})

	require.True(t, cache.IsRecordingStats())

	cache = Must(&Options[int, int]{
		StatsRecorder: &stats.NoopRecorder{},
	})

	require.False(t, cache.IsRecordingStats())
}

type fakeRecorder struct {
	hits   atomic.Uint64
	misses atomic.Uint64
}

func (f *fakeRecorder) RecordHits(count int) {
	f.hits.Add(uint64(count))
}

func (f *fakeRecorder) RecordMisses(count int) {
	f.misses.Add(uint64(count))
}

func (f *fakeRecorder) RecordEviction(weight uint32) {
	panic("implement me")
}

func (f *fakeRecorder) RecordLoadSuccess(loadTime time.Duration) {
	panic("implement me")
}

func (f *fakeRecorder) RecordLoadFailure(loadTime time.Duration) {
	panic("implement me")
}

func TestCache_Stats(t *testing.T) {
	t.Parallel()

	counter := stats.NewCounter()
	cache := Must(&Options[int, int]{
		StatsRecorder: counter,
	})

	for i := 0; i < 100; i++ {
		cache.Set(i, i)
		cache.GetIfPresent(i)
	}

	snapshot := counter.Snapshot()
	require.Equal(t, uint64(100), snapshot.Hits)
	require.Equal(t, uint64(0), snapshot.Misses)
	require.Equal(t, snapshot, cache.Stats())

	fr := &fakeRecorder{}
	cache = Must(&Options[int, int]{
		StatsRecorder: fr,
	})

	for i := 0; i < 100; i++ {
		cache.Set(i, i)
		cache.GetIfPresent(i)
	}

	require.Equal(t, uint64(100), fr.hits.Load())
	require.Equal(t, uint64(0), fr.misses.Load())
	require.Equal(t, stats.Stats{}, cache.Stats())
}

func TestCache_Ratio(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	m := make(map[DeletionCause]int)
	statsCounter := stats.NewCounter()
	capacity := 100
	c := Must(&Options[uint64, uint64]{
		MaximumSize:   capacity,
		StatsRecorder: statsCounter,
		Executor: func(fn func()) {
			fn()
		},
		OnDeletion: func(e DeletionEvent[uint64, uint64]) {
			mutex.Lock()
			m[e.Cause]++
			mutex.Unlock()
		},
	})

	z := rand.NewZipf(rand.New(rand.NewSource(time.Now().UnixNano())), 1.0001, 1, 1000)

	o := newOptimal(100)
	for i := 0; i < 10000; i++ {
		k := z.Uint64()

		o.Get(k)
		if !c.has(k) {
			c.Set(k, k)
		}
	}

	t.Logf("actual size: %d, capacity: %d", c.EstimatedSize(), capacity)
	t.Logf("actual: %.2f, optimal: %.2f", statsCounter.Snapshot().HitRatio(), o.Ratio())

	if size := c.EstimatedSize(); size != capacity {
		t.Fatalf("not valid cache size. expected %d, but got %d", capacity, size)
	}

	mutex.Lock()
	defer mutex.Unlock()
	t.Logf("evicted: %d", m[CauseOverflow])
	if len(m) != 1 || m[CauseOverflow] <= 0 || m[CauseOverflow] > 5000 {
		t.Fatalf("cache was supposed to evict positive number of entries, but evicted %d entries", m[CauseOverflow])
	}
}

func TestCache_ValidateFunctions(t *testing.T) {
	t.Parallel()

	getPublicMethods := func(v any) map[string]bool {
		tp := reflect.TypeOf(v)

		res := make(map[string]bool, tp.NumMethod())
		for i := 0; i < tp.NumMethod(); i++ {
			method := tp.Method(i)
			res[method.Name] = true
		}
		return res
	}

	publicMethods := getPublicMethods(&Cache[int, int]{})
	implPublicMethods := getPublicMethods(&cache[int, int]{})

	for m := range publicMethods {
		if implPublicMethods[m] {
			continue
		}
		t.Errorf("Cache has an unknown %s method", m)
	}
	for m := range implPublicMethods {
		if publicMethods[m] {
			continue
		}
		t.Errorf("cache has an unknown %s method", m)
	}
}

type optimal struct {
	capacity uint64
	hits     map[uint64]uint64
	access   []uint64
}

func newOptimal(capacity uint64) *optimal {
	return &optimal{
		capacity: capacity,
		hits:     make(map[uint64]uint64),
		access:   make([]uint64, 0),
	}
}

func (o *optimal) Get(key uint64) {
	o.hits[key]++
	o.access = append(o.access, key)
}

func (o *optimal) Ratio() float64 {
	look := make(map[uint64]struct{}, o.capacity)
	data := &optimalHeap{}
	heap.Init(data)
	hits := 0
	misses := 0
	for _, key := range o.access {
		if _, has := look[key]; has {
			hits++
			continue
		}
		if uint64(data.Len()) >= o.capacity {
			victim := heap.Pop(data)
			delete(look, victim.(*optimalItem).key)
		}
		misses++
		look[key] = struct{}{}
		heap.Push(data, &optimalItem{key, o.hits[key]})
	}
	if hits == 0 && misses == 0 {
		return 0.0
	}
	return float64(hits) / float64(hits+misses)
}

type optimalItem struct {
	key  uint64
	hits uint64
}

type optimalHeap []*optimalItem

func (h optimalHeap) Len() int           { return len(h) }
func (h optimalHeap) Less(i, j int) bool { return h[i].hits < h[j].hits }
func (h optimalHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *optimalHeap) Push(x any) {
	*h = append(*h, x.(*optimalItem))
}

func (h *optimalHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
