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

package master

import (
	"context"
	"time"

	"github.com/gorse-io/gorse/model/ctr"
	"github.com/gorse-io/gorse/storage/cache"
	"github.com/gorse-io/gorse/storage/data"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CVRMetricAvg = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "gorse",
		Subsystem: "master",
		Name:      "cvr_average",
		Help:      "Average CVR (chat/match ratio) across all items with match events",
	})
	CVRMetricCount = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "gorse",
		Subsystem: "master",
		Name:      "cvr_item_count",
		Help:      "Number of items with match events",
	})
)

// CVRReporter periodically computes the average CVR across items with match events
// and reports it to both Prometheus gauges and the cache Time Series.
type CVRReporter struct {
	cacheClient  cache.Database
	dataClient   data.Database
	cvrModel     *ctr.CVRModel
	interval     time.Duration
	stopCh       chan struct{}
}

// NewCVRReporter creates a new CVRReporter.
func NewCVRReporter(cacheClient cache.Database, dataClient data.Database, interval time.Duration) *CVRReporter {
	return &CVRReporter{
		cacheClient: cacheClient,
		dataClient:  dataClient,
		cvrModel:    ctr.NewCVRModel(dataClient),
		interval:    interval,
		stopCh:      make(chan struct{}),
	}
}

// Start launches the background reporting loop.
func (r *CVRReporter) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				r.report(ctx)
			case <-r.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop gracefully stops the reporting loop.
func (r *CVRReporter) Stop() {
	close(r.stopCh)
}

// report computes CVR stats for items with match events and reports to Prometheus + Time Series.
func (r *CVRReporter) report(ctx context.Context) {
	avg, count := r.computeCVRStats(ctx)

	CVRMetricAvg.Set(avg)
	CVRMetricCount.Set(count)

	now := time.Now()
	_ = r.cacheClient.AddTimeSeriesPoints(ctx, []cache.TimeSeriesPoint{
		{Name: cache.CVRAverage, Value: avg, Timestamp: now},
		{Name: cache.CVRCount, Value: count, Timestamp: now},
	})
}

// computeCVRStats computes the average CVR across items that have match events.
// It iterates over user items (M/F/O category), calculates chat/match ratio for each,
// and returns the mean CVR and count.
func (r *CVRReporter) computeCVRStats(ctx context.Context) (avg float64, count float64) {
	// Collect all user items by iterating with cursor.
	var allItems []*data.Item
	cursor := ""
	for {
		_, items, err := r.dataClient.GetItems(ctx, cursor, 500, nil)
		if err != nil {
			return 0, 0
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if len(item.Categories) > 0 {
				cat := item.Categories[0]
				if cat == "M" || cat == "F" || cat == "O" {
					allItems = append(allItems, &item)
				}
			}
		}
		cursor = items[len(items)-1].ItemId
		if len(items) < 500 {
			break
		}
	}

	if len(allItems) == 0 {
		return 0, 0
	}

	// Use the CVR model to compute per-item CVR scores.
	scores, err := r.cvrModel.ComputeCVRScores(ctx, allItems)
	if err != nil {
		return 0, 0
	}

	// Compute average CVR across items that have match events.
	var sum float64
	var matchedCount int
	for _, item := range allItems {
		if cvr, ok := scores[item.ItemId]; ok && cvr > 0 {
			sum += cvr
			matchedCount++
		}
	}

	if matchedCount == 0 {
		return 0, 0
	}
	return sum / float64(matchedCount), float64(matchedCount)
}
