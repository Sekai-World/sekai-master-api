package observability

import (
	"context"
	goruntime "runtime"
	"sort"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"

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

func RegisterMasterDataMetrics(syncUsecase *usecase.MasterDataSyncUsecase) error {
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

	regionRecords, err := meter.Int64ObservableGauge(
		"sekai_master_data_region_records",
		apimetric.WithDescription("Number of master data records stored for a region; 0 means the store holds no data for it."),
	)
	if err != nil {
		return err
	}

	leaseHeld, err := meter.Int64ObservableGauge(
		"sekai_master_data_sync_lease_held",
		apimetric.WithDescription("Whether the cross-pod master data sync lease is currently held by any owner."),
	)
	if err != nil {
		return err
	}

	leaseToken, err := meter.Int64ObservableGauge(
		"sekai_master_data_sync_lease_fencing_token",
		apimetric.WithDescription("Current fencing token of the master data sync lease; 0 when the lease is not held."),
	)
	if err != nil {
		return err
	}

	leaseExpiry, err := meter.Int64ObservableGauge(
		"sekai_master_data_sync_lease_expiry_unix",
		apimetric.WithDescription("Unix timestamp when the held master data sync lease expires; 0 when unheld."),
		apimetric.WithUnit("s"),
	)
	if err != nil {
		return err
	}

	leaseTakeovers, err := meter.Int64Counter(
		"sekai_master_data_sync_lease_takeovers",
		apimetric.WithDescription("Observed fencing-token increases across metric reads; approximates lease takeovers seen by this process."),
		apimetric.WithUnit("{takeover}"),
	)
	if err != nil {
		return err
	}
	lastObservedLeaseToken := int64(0)

	_, err = meter.RegisterCallback(func(ctx context.Context, observer apimetric.Observer) error {
		regionNames := make([]string, 0)
		statusByRegion := make(map[string]string)
		fileCountByRegion := make(map[string]int64)
		durationByRegion := make(map[string]int64)
		lastSyncedByRegion := make(map[string]int64)
		var recordsByRegion map[string]int64

		if syncUsecase != nil {
			configured := syncUsecase.ConfiguredRegions()
			regionNames = append(regionNames, configured...)
			observer.ObserveInt64(configuredRegions, int64(len(configured)))

			if syncUsecase.IsSyncRunning() {
				observer.ObserveInt64(syncRunning, 1)
			} else {
				observer.ObserveInt64(syncRunning, 0)
			}

			leaseState, leaseErr := syncUsecase.SyncLeaseState(ctx)
			if leaseErr == nil {
				if leaseState.Held {
					observer.ObserveInt64(leaseHeld, 1)
					observer.ObserveInt64(leaseToken, leaseState.Token)
					observer.ObserveInt64(leaseExpiry, leaseState.ExpiresAt.Unix())
				} else {
					observer.ObserveInt64(leaseHeld, 0)
					observer.ObserveInt64(leaseToken, 0)
					observer.ObserveInt64(leaseExpiry, 0)
				}
				// A token increase between reads means another owner took the
				// lease (or a released lease was re-acquired); count the
				// observed takeovers for diagnostics. Read from the callback
				// only, so no extra synchronization is needed.
				if leaseState.Token > lastObservedLeaseToken && lastObservedLeaseToken > 0 {
					leaseTakeovers.Add(ctx, 1)
				}
				if leaseState.Token > lastObservedLeaseToken {
					lastObservedLeaseToken = leaseState.Token
				}
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

		if syncUsecase != nil {
			counts, err := syncUsecase.RegionRecordCounts(ctx)
			if err == nil {
				recordsByRegion = counts
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

			if recordsByRegion != nil {
				observer.ObserveInt64(regionRecords, recordsByRegion[region], attrs)
			}
		}

		return nil
	},
		syncRunning,
		configuredRegions,
		regionStatus,
		regionFileCount,
		regionSyncDuration,
		regionLastSynced,
		regionRecords,
		leaseHeld,
		leaseToken,
		leaseExpiry,
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
