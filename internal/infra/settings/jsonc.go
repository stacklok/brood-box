// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

// stripJSONC removes // and /* */ comments and trailing commas from JSONC
// data while respecting string literals. Returns valid JSON.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	i := 0
	n := len(data)

	for i < n {
		// Inside a string literal: copy until closing quote.
		if data[i] == '"' {
			out = append(out, data[i])
			i++
			for i < n {
				if data[i] == '\\' && i+1 < n {
					out = append(out, data[i], data[i+1])
					i += 2
					continue
				}
				out = append(out, data[i])
				if data[i] == '"' {
					i++
					break
				}
				i++
			}
			continue
		}

		// Line comment: skip until end of line.
		if i+1 < n && data[i] == '/' && data[i+1] == '/' {
			i += 2
			for i < n && data[i] != '\n' {
				i++
			}
			continue
		}

		// Block comment: skip until closing */.
		if i+1 < n && data[i] == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < n {
				if data[i] == '*' && data[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}

		out = append(out, data[i])
		i++
	}

	// Strip trailing commas before } and ].
	return stripTrailingCommas(out)
}

// stripTrailingCommas removes commas immediately followed by } or ]
// (with optional whitespace between).
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	i := 0
	n := len(data)

	for i < n {
		// Inside a string literal: copy verbatim.
		if data[i] == '"' {
			out = append(out, data[i])
			i++
			for i < n {
				if data[i] == '\\' && i+1 < n {
					out = append(out, data[i], data[i+1])
					i += 2
					continue
				}
				out = append(out, data[i])
				if data[i] == '"' {
					i++
					break
				}
				i++
			}
			continue
		}

		if data[i] == ',' {
			// Look ahead past whitespace for } or ].
			j := i + 1
			for j < n && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < n && (data[j] == '}' || data[j] == ']') {
				// Skip the comma, keep the whitespace and closing bracket.
				i++
				continue
			}
		}

		out = append(out, data[i])
		i++
	}

	return out
}
