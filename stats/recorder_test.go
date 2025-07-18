// Copyright (c) 2025 Alexey Mayshev and contributors. All rights reserved.
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

package stats

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNoopRecorder(t *testing.T) {
	t.Parallel()

	var i Recorder = &NoopRecorder{}
	require.NotPanics(t, func() {
		i.RecordHits(1)
		i.RecordMisses(1)
		i.RecordEviction(5)
		i.RecordLoadSuccess(time.Hour)
		i.RecordLoadFailure(time.Minute)
	})
}
