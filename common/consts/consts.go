// Copyright 2025 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package consts

import "time"

// Dataset loading and processing.
const (
	// DefaultBatchSize is the default batch size for dataset loading.
	DefaultBatchSize = 10000
)

// Cache and search.
const (
	// DefaultMaxSearchResults is the default maximum number of search results.
	DefaultMaxSearchResults = 10000
)

// Meta store.
const (
	// DefaultMetaTimeout is the default timeout for meta store operations.
	DefaultMetaTimeout = 10 * time.Second
)

// Cache TTL.
const (
	// DefaultCacheTTL is the default TTL for cached recommendations.
	DefaultCacheTTL = 72 * time.Hour
	// DefaultCacheSize is the default number of cached recommendations.
	DefaultCacheSize = 100
)

// HTTP client.
const (
	// DefaultHTTPTimeout is the default timeout for external HTTP requests.
	DefaultHTTPTimeout = 10 * time.Second
)
