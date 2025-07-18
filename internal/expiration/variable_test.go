// Copyright (c) 2024 Alexey Mayshev and contributors. All rights reserved.
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

package expiration

import (
	"testing"
	"time"

	"github.com/maypok86/otter/v2/internal/generated/node"
)

func getTestExp(sec int64) int64 {
	return (time.Duration(sec) * time.Second).Nanoseconds()
}

func contains[K comparable, V any](root, f node.Node[K, V]) bool {
	n := root.NextExp()
	for !node.Equals(n, root) {
		if node.Equals(n, f) {
			return true
		}

		n = n.NextExp()
	}
	return false
}

func match[K comparable, V any](t *testing.T, nodes []node.Node[K, V], keys []K) {
	t.Helper()

	if len(nodes) != len(keys) {
		t.Fatalf("Not equals lengths of nodes (%d) and keys (%d)", len(nodes), len(keys))
	}

	for i, k := range keys {
		if k != nodes[i].Key() {
			t.Fatalf("Not valid entry found: %+v", nodes[i])
		}
	}
}

func TestVariable_Add(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", getTestExp(1), 0, 1),
		nm.Create("k2", "", getTestExp(69), 0, 1),
		nm.Create("k3", "", getTestExp(4399), 0, 1),
	}
	v := NewVariable(nm)

	for _, n := range nodes {
		v.Add(n)
	}

	var found bool
	for _, root := range v.wheel[0] {
		if contains(root, nodes[0]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[0])
	}

	found = false
	for _, root := range v.wheel[1] {
		if contains(root, nodes[1]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[1])
	}

	found = false
	for _, root := range v.wheel[2] {
		if contains(root, nodes[2]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Not found node %+v in timer wheel", nodes[2])
	}
}

func TestVariable_DeleteExpired(t *testing.T) {
	t.Parallel()

	nm := node.NewManager[string, string](node.Config{
		WithExpiration: true,
	})
	now := time.Now().UnixNano()
	nodes := []node.Node[string, string]{
		nm.Create("k1", "", now+getTestExp(1), 0, 1),
		nm.Create("k2", "", now+getTestExp(10), 0, 1),
		nm.Create("k3", "", now+getTestExp(30), 0, 1),
		nm.Create("k4", "", now+getTestExp(120), 0, 1),
		nm.Create("k5", "", now+getTestExp(6500), 0, 1),
		nm.Create("k6", "", now+getTestExp(142000), 0, 1),
		nm.Create("k7", "", now+getTestExp(1420000), 0, 1),
	}
	var expired []node.Node[string, string]
	expireNode := func(n node.Node[string, string], nowNanos int64) {
		expired = append(expired, n)
	}
	v := NewVariable(nm)
	v.time = uint64(now)

	for _, n := range nodes {
		v.Add(n)
	}

	var keys []string

	v.DeleteExpired(now+getTestExp(2), expireNode)
	keys = append(keys, "k1")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(64), expireNode)
	keys = append(keys, "k2", "k3")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(121), expireNode)
	keys = append(keys, "k4")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(12000), expireNode)
	keys = append(keys, "k5")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(350000), expireNode)
	keys = append(keys, "k6")
	match(t, expired, keys)

	v.DeleteExpired(now+getTestExp(1520000), expireNode)
	keys = append(keys, "k7")
	match(t, expired, keys)
}
