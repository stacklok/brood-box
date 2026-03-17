// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

// FilterEntries returns entries matching the predicate.
func FilterEntries(entries []Entry, keep func(Entry) bool) []Entry {
	var result []Entry
	for _, e := range entries {
		if keep(e) {
			result = append(result, e)
		}
	}
	return result
}
