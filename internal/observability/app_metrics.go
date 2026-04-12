package observability

import (
	"context"
	goruntime "runtime"
	"sort"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"

	"sekai-master-api/internal/storage"
	"sekai-master-api/internal/usecase"
)

func RegisterRuntimeMetrics() error {
	meter := otel.Meter("sekai-master-api/runtime")

	heapAlloc, err := meter.Int64ObservableGauge(
		"sekai_go_mem_heap_alloc_bytes",
		apimetric.WithDescription("Current Go heap allocation in bytes."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	sysBytes, err := meter.Int64ObservableGauge(
		"sekai_go_mem_sys_bytes",
		apimetric.WithDescription("Total bytes of memory obtained from the OS by the Go runtime."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	stackInUse, err := meter.Int64ObservableGauge(
		"sekai_go_mem_stack_inuse_bytes",
		apimetric.WithDescription("Go runtime stack memory in use in bytes."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	goroutines, err := meter.Int64ObservableGauge(
		"sekai_go_goroutines",
		apimetric.WithDescription("Number of active goroutines."),
	)
	if err != nil {
		return err
	}

	lastGCPause, err := meter.Int64ObservableGauge(
		"sekai_go_gc_last_pause_ns",
		apimetric.WithDescription("Last observed Go GC pause in nanoseconds."),
		apimetric.WithUnit("ns"),
	)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer apimetric.Observer) error {
		var memStats goruntime.MemStats
		goruntime.ReadMemStats(&memStats)

		observer.ObserveInt64(heapAlloc, int64(memStats.HeapAlloc))
		observer.ObserveInt64(sysBytes, int64(memStats.Sys))
		observer.ObserveInt64(stackInUse, int64(memStats.StackInuse))
		observer.ObserveInt64(goroutines, int64(goruntime.NumGoroutine()))
		observer.ObserveInt64(lastGCPause, int64(memStats.PauseNs[(memStats.NumGC+255)%256]))

		return nil
	}, heapAlloc, sysBytes, stackInUse, goroutines, lastGCPause)

	return err
}

func RegisterMasterDataMetrics(syncUsecase *usecase.MasterDataSyncUsecase, cache *storage.RedisMasterDataCache) error {
	meter := otel.Meter("sekai-master-api/master-data")

	syncRunning, err := meter.Int64ObservableGauge(
		"sekai_master_data_sync_running",
		apimetric.WithDescription("Whether any master data sync is currently running."),
	)
	if err != nil {
		return err
	}

	configuredRegions, err := meter.Int64ObservableGauge(
		"sekai_master_data_configured_regions",
		apimetric.WithDescription("Number of configured master data regions."),
	)
	if err != nil {
		return err
	}

	regionStatus, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_status",
		apimetric.WithDescription("Current master data status for a region; value is always 1 and labels identify the status."),
	)
	if err != nil {
		return err
	}

	regionFileCount, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_file_count",
		apimetric.WithDescription("Last known synced file count per region."),
	)
	if err != nil {
		return err
	}

	regionSyncDuration, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_sync_duration_ms",
		apimetric.WithDescription("Last known sync duration per region in milliseconds."),
		apimetric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	regionLastSynced, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_last_synced_unix",
		apimetric.WithDescription("Unix timestamp of the last sync per region."),
		apimetric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	indexLoaded, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_loaded",
		apimetric.WithDescription("Whether a region search index is loaded in memory."),
	)
	if err != nil {
		return err
	}

	indexEntities, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_entities",
		apimetric.WithDescription("Number of indexed entities per region."),
	)
	if err != nil {
		return err
	}

	indexRecords, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_records",
		apimetric.WithDescription("Number of indexed records per region."),
	)
	if err != nil {
		return err
	}

	indexFields, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_fields",
		apimetric.WithDescription("Number of indexed searchable fields per region."),
	)
	if err != nil {
		return err
	}

	indexEntries, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_entries",
		apimetric.WithDescription("Number of search index entries per region."),
	)
	if err != nil {
		return err
	}

	indexTextBlobBytes, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_text_blob_bytes",
		apimetric.WithDescription("Bytes used by the in-memory search text blob per region."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	indexApproxBytes, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_index_approx_size_bytes",
		apimetric.WithDescription("Approximate bytes used by the in-memory region search index."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	redisUsedMemory, err := meter.Int64ObservableGauge(
		"sekai_redis_used_memory_bytes",
		apimetric.WithDescription("Redis used memory in bytes."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	redisUsedMemoryRSS, err := meter.Int64ObservableGauge(
		"sekai_redis_used_memory_rss_bytes",
		apimetric.WithDescription("Redis resident set size memory in bytes."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	redisPeakMemory, err := meter.Int64ObservableGauge(
		"sekai_redis_peak_memory_bytes",
		apimetric.WithDescription("Redis peak memory in bytes."),
		apimetric.WithUnit("By"),
	)
	if err != nil {
		return err
	}

	redisKeys, err := meter.Int64ObservableGauge(
		"sekai_redis_keys",
		apimetric.WithDescription("Approximate number of keys in the configured Redis database."),
	)
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer apimetric.Observer) error {
		regionNames := make([]string, 0)
		statusByRegion := make(map[string]string)
		fileCountByRegion := make(map[string]int64)
		durationByRegion := make(map[string]int64)
		lastSyncedByRegion := make(map[string]int64)

		if syncUsecase != nil {
			configured := syncUsecase.ConfiguredRegions()
			regionNames = append(regionNames, configured...)
			observer.ObserveInt64(configuredRegions, int64(len(configured)))

			if syncUsecase.IsSyncRunning() {
				observer.ObserveInt64(syncRunning, 1)
			} else {
				observer.ObserveInt64(syncRunning, 0)
			}

			statuses, err := syncUsecase.DashboardStatus(ctx)
			if err == nil {
				for _, status := range statuses {
					region := strings.ToLower(strings.TrimSpace(status.Region))
					if region == "" {
						continue
					}

					statusByRegion[region] = strings.ToLower(strings.TrimSpace(status.Status))
					fileCountByRegion[region] = int64(status.FileCount)
					durationByRegion[region] = status.SyncDurationMS
					if !status.LastSyncedAt.IsZero() {
						lastSyncedByRegion[region] = status.LastSyncedAt.Unix()
					}
				}
			}
		} else {
			observer.ObserveInt64(syncRunning, 0)
			observer.ObserveInt64(configuredRegions, 0)
		}

		indexStatsByRegion := make(map[string]storage.RegionIndexStats)
		if cache != nil {
			for _, stat := range cache.RegionIndexStats() {
				indexStatsByRegion[stat.Region] = stat
				regionNames = append(regionNames, stat.Region)
			}

			redisStats, err := cache.RedisUsageStats(ctx)
			if err == nil {
				observer.ObserveInt64(redisUsedMemory, redisStats.UsedMemoryBytes)
				observer.ObserveInt64(redisUsedMemoryRSS, redisStats.UsedMemoryRSSBytes)
				observer.ObserveInt64(redisPeakMemory, redisStats.PeakMemoryBytes)
				observer.ObserveInt64(redisKeys, redisStats.KeyCount)
			}
		}

		for _, region := range uniqueStrings(regionNames) {
			attrs := apimetric.WithAttributes(attribute.String("region", region))

			status := statusByRegion[region]
			if status == "" {
				status = "unknown"
			}

			observer.ObserveInt64(regionStatus, 1, apimetric.WithAttributes(
				attribute.String("region", region),
				attribute.String("status", status),
			))
			observer.ObserveInt64(regionFileCount, fileCountByRegion[region], attrs)
			observer.ObserveInt64(regionSyncDuration, durationByRegion[region], attrs)
			observer.ObserveInt64(regionLastSynced, lastSyncedByRegion[region], attrs)

			indexStat, ok := indexStatsByRegion[region]
			if ok && indexStat.Loaded {
				observer.ObserveInt64(indexLoaded, 1, attrs)
				observer.ObserveInt64(indexEntities, int64(indexStat.EntityCount), attrs)
				observer.ObserveInt64(indexRecords, int64(indexStat.RecordCount), attrs)
				observer.ObserveInt64(indexFields, int64(indexStat.FieldCount), attrs)
				observer.ObserveInt64(indexEntries, int64(indexStat.EntryCount), attrs)
				observer.ObserveInt64(indexTextBlobBytes, int64(indexStat.TextBlobBytes), attrs)
				observer.ObserveInt64(indexApproxBytes, int64(indexStat.ApproxSizeBytes), attrs)
				continue
			}

			observer.ObserveInt64(indexLoaded, 0, attrs)
			observer.ObserveInt64(indexEntities, 0, attrs)
			observer.ObserveInt64(indexRecords, 0, attrs)
			observer.ObserveInt64(indexFields, 0, attrs)
			observer.ObserveInt64(indexEntries, 0, attrs)
			observer.ObserveInt64(indexTextBlobBytes, 0, attrs)
			observer.ObserveInt64(indexApproxBytes, 0, attrs)
		}

		return nil
	},
		syncRunning,
		configuredRegions,
		regionStatus,
		regionFileCount,
		regionSyncDuration,
		regionLastSynced,
		indexLoaded,
		indexEntities,
		indexRecords,
		indexFields,
		indexEntries,
		indexTextBlobBytes,
		indexApproxBytes,
		redisUsedMemory,
		redisUsedMemoryRSS,
		redisPeakMemory,
		redisKeys,
	)

	return err
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	sort.Strings(result)
	return result
}
