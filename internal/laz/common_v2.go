// Copyright (c) 2007-2022 rapidlasso GmbH - fast tools to catch reality (Original C++ implementation)
// Copyright (c) 2026 Massimo Federico Bonfigli (Go port and modifications)
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
//
// This file is a Go port of LASzip (https://github.com/LASzip/LASzip).
// Changes: translated from C++ to Go.

// Package laz provides LAZ (LASzip) decompression for LAS point cloud data.
//
// common_v2.go — v2 shared types (StreamingMedian5, return maps) ported from
// src/laszip_common_v2.hpp. Read-relevant subset only.
package laz

// StreamingMedian5 maintains a sorted list of the last 5 values and provides
// the median (the 3rd element). Used by v2/v3/v4 compressed readers to track
// X and Y differences for context-based prediction.
type StreamingMedian5 struct {
	Values [5]int32
	High   bool
}

// Init resets the median tracker.
func (m *StreamingMedian5) Init() {
	m.Values[0] = 0
	m.Values[1] = 0
	m.Values[2] = 0
	m.Values[3] = 0
	m.Values[4] = 0
	m.High = true
}

// Add inserts v into the sorted list, maintaining median order.
func (m *StreamingMedian5) Add(v int32) {
	if m.High {
		if v < m.Values[2] {
			m.Values[4] = m.Values[3]
			m.Values[3] = m.Values[2]
			if v < m.Values[0] {
				m.Values[2] = m.Values[1]
				m.Values[1] = m.Values[0]
				m.Values[0] = v
			} else if v < m.Values[1] {
				m.Values[2] = m.Values[1]
				m.Values[1] = v
			} else {
				m.Values[2] = v
			}
		} else {
			if v < m.Values[3] {
				m.Values[4] = m.Values[3]
				m.Values[3] = v
			} else {
				m.Values[4] = v
			}
			m.High = false
		}
	} else {
		if m.Values[2] < v {
			m.Values[0] = m.Values[1]
			m.Values[1] = m.Values[2]
			if m.Values[4] < v {
				m.Values[2] = m.Values[3]
				m.Values[3] = m.Values[4]
				m.Values[4] = v
			} else if m.Values[3] < v {
				m.Values[2] = m.Values[3]
				m.Values[3] = v
			} else {
				m.Values[2] = v
			}
		} else {
			if m.Values[1] < v {
				m.Values[0] = m.Values[1]
				m.Values[1] = v
			} else {
				m.Values[0] = v
			}
			m.High = true
		}
	}
}

// Get returns the current median (values[2]).
func (m *StreamingMedian5) Get() int32 {
	return m.Values[2]
}

// NewStreamingMedian5 creates and initializes a new StreamingMedian5.
func NewStreamingMedian5() *StreamingMedian5 {
	m := &StreamingMedian5{}
	m.Init()
	return m
}

// numberReturnMap maps (number_of_returns, return_number) to a context
// index (0–15) for v2 compression. 8×8 table from laszip_common_v2.hpp.
var NumberReturnMap = [8][8]uint8{
	{15, 14, 13, 12, 11, 10, 9, 8},
	{14, 0, 1, 3, 6, 10, 10, 9},
	{13, 1, 2, 4, 7, 11, 11, 10},
	{12, 3, 4, 5, 8, 12, 12, 11},
	{11, 6, 7, 8, 9, 13, 13, 12},
	{10, 10, 11, 12, 13, 14, 14, 13},
	{9, 10, 11, 12, 13, 14, 15, 14},
	{8, 9, 10, 11, 12, 13, 14, 15},
}

// numberReturnLevel maps (number_of_returns, return_number) to a return
// level context (0–7). 8×8 table from laszip_common_v2.hpp.
var NumberReturnLevel = [8][8]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7},
	{1, 0, 1, 2, 3, 4, 5, 6},
	{2, 1, 0, 1, 2, 3, 4, 5},
	{3, 2, 1, 0, 1, 2, 3, 4},
	{4, 3, 2, 1, 0, 1, 2, 3},
	{5, 4, 3, 2, 1, 0, 1, 2},
	{6, 5, 4, 3, 2, 1, 0, 1},
	{7, 6, 5, 4, 3, 2, 1, 0},
}
