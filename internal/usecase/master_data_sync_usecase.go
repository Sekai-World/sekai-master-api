package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
)

type MasterDataSourceLoader interface {
	LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error)
}

type MasterDataSourceVersionResolver interface {
	ResolveRegionVersion(ctx context.Context, source masterdata.Source) (string, error)
}

type MasterDataCache interface {
	StoreRegion(ctx context.Context, region string, payload map[string]any) error
	GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)
	ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error)
	ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error)
	Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error)
}

type MasterDataCacheIndexRebuilder interface {
	RebuildRegionIndexFromRedis(ctx context.Context, region string) (bool, error)
}

type MasterDataCacheIndexLoader interface {
	LoadRegionIndexFromRedis(ctx context.Context, region string) (bool, error)
}

type MasterDataCacheIndexInspector interface {
	HasRegionIndex(region string) bool
}

type MasterDataSyncStatusStore interface {
	Save(ctx context.Context, status masterdata.SyncStatus) error
	List(ctx context.Context) ([]masterdata.SyncStatus, error)
}

type MasterDataSyncLatestSuccessStore interface {
	ListLatestSuccess(ctx context.Context) ([]masterdata.SyncStatus, error)
}

type MasterDataSyncLatestStableStore interface {
	ListLatestStable(ctx context.Context) ([]masterdata.SyncStatus, error)
}

type MasterDataEventPublisher interface {
	PublishMasterDataUpdated(ctx context.Context, event masterdata.SyncUpdatedEvent) error
}

type MasterDataPayloadBackupStore interface {
	SaveRegionPayload(ctx context.Context, source masterdata.Source, commit string, payload map[string]any) error
	LoadRegionPayload(ctx context.Context, source masterdata.Source, commit string) (map[string]any, bool, error)
	LoadLatestRegionPayload(ctx context.Context, source masterdata.Source) (map[string]any, string, time.Time, bool, error)
}

type MasterDataSyncUsecase struct {
	sources                             []masterdata.Source
	loader                              MasterDataSourceLoader
	cache                               MasterDataCache
	statusStore                         MasterDataSyncStatusStore
	publisher                           MasterDataEventPublisher
	backupStore                         MasterDataPayloadBackupStore
	concurrency                         int
	regionTimeout                       time.Duration
	restoreFromLocalBackupWithoutStatus bool
	statusMu                            sync.Mutex
	syncRunning                         atomic.Bool

	currentEventLocks sync.Map
}

const masterDataSyncLogComponent = "master-data-sync"

var ErrSyncInProgress = errors.New("master data sync is already running")
var ErrRegionNotFound = errors.New("master data region not found")

func NewMasterDataSyncUsecase(
	sources []masterdata.Source,
	loader MasterDataSourceLoader,
	cache MasterDataCache,
	statusStore MasterDataSyncStatusStore,
	publisher MasterDataEventPublisher,
	concurrency int,
) *MasterDataSyncUsecase {
	if concurrency <= 0 {
		concurrency = 1
	}

	return &MasterDataSyncUsecase{
		sources:     sources,
		loader:      loader,
		cache:       cache,
		statusStore: statusStore,
		publisher:   publisher,
		backupStore: NewFileMasterDataPayloadBackupStore("tmp/master-data-backup"),
		concurrency: concurrency,
	}
}

func (usecase *MasterDataSyncUsecase) EnableDevelopmentBackupBootstrap(enabled bool) {
	if usecase == nil {
		return
	}

	usecase.restoreFromLocalBackupWithoutStatus = enabled
}

func (usecase *MasterDataSyncUsecase) SetRegionTimeout(timeout time.Duration) {
	if usecase == nil {
		return
	}

	if timeout < 0 {
		timeout = 0
	}
	usecase.regionTimeout = timeout
}

func (usecase *MasterDataSyncUsecase) SyncAll(ctx context.Context) error {
	return usecase.sync(ctx, false, usecase.sources)
}

func (usecase *MasterDataSyncUsecase) SyncAllForce(ctx context.Context) error {
	return usecase.sync(ctx, true, usecase.sources)
}

func (usecase *MasterDataSyncUsecase) SyncRegion(ctx context.Context, region string) error {
	targetRegion := strings.ToLower(strings.TrimSpace(region))
	if targetRegion == "" {
		return ErrRegionNotFound
	}

	targetSources := make([]masterdata.Source, 0, 1)
	for _, source := range usecase.sources {
		if strings.EqualFold(strings.TrimSpace(source.Region), targetRegion) {
			targetSources = append(targetSources, source)
			break
		}
	}

	if len(targetSources) == 0 {
		return ErrRegionNotFound
	}

	return usecase.sync(ctx, false, targetSources)
}

func (usecase *MasterDataSyncUsecase) SyncRegionForce(ctx context.Context, region string) error {
	targetRegion := strings.ToLower(strings.TrimSpace(region))
	if targetRegion == "" {
		return ErrRegionNotFound
	}

	targetSources := make([]masterdata.Source, 0, 1)
	for _, source := range usecase.sources {
		if strings.EqualFold(strings.TrimSpace(source.Region), targetRegion) {
			targetSources = append(targetSources, source)
			break
		}
	}

	if len(targetSources) == 0 {
		return ErrRegionNotFound
	}

	return usecase.sync(ctx, true, targetSources)
}

func (usecase *MasterDataSyncUsecase) InterruptedRegions(ctx context.Context) ([]string, error) {
	if usecase.statusStore == nil {
		return []string{}, nil
	}

	statuses, err := usecase.statusStore.List(ctx)
	if err != nil {
		return nil, err
	}

	configured := make(map[string]struct{}, len(usecase.sources))
	for _, source := range usecase.sources {
		region := strings.ToLower(strings.TrimSpace(source.Region))
		if region == "" {
			continue
		}
		configured[region] = struct{}{}
	}

	regions := make([]string, 0)
	seen := make(map[string]struct{})
	for _, status := range statuses {
		region := strings.ToLower(strings.TrimSpace(status.Region))
		if region == "" {
			continue
		}
		if _, ok := configured[region]; !ok {
			continue
		}

		normalizedStatus := strings.ToLower(strings.TrimSpace(status.Status))
		if normalizedStatus != "running" && normalizedStatus != "pending" {
			continue
		}

		if _, ok := seen[region]; ok {
			continue
		}
		seen[region] = struct{}{}
		regions = append(regions, region)
	}

	sort.Strings(regions)
	return regions, nil
}

func (usecase *MasterDataSyncUsecase) RecoverInterruptedSync(ctx context.Context) ([]string, error) {
	regions, err := usecase.InterruptedRegions(ctx)
	if err != nil {
		return nil, err
	}
	if len(regions) == 0 {
		return []string{}, nil
	}

	targetRegions := make(map[string]struct{}, len(regions))
	for _, region := range regions {
		targetRegions[region] = struct{}{}
	}

	targetSources := make([]masterdata.Source, 0, len(regions))
	for _, source := range usecase.sources {
		region := strings.ToLower(strings.TrimSpace(source.Region))
		if _, ok := targetRegions[region]; ok {
			targetSources = append(targetSources, source)
		}
	}

	if len(targetSources) == 0 {
		return []string{}, nil
	}

	return regions, usecase.sync(ctx, false, targetSources)
}

func (usecase *MasterDataSyncUsecase) sync(ctx context.Context, force bool, sources []masterdata.Source) error {
	if !usecase.syncRunning.CompareAndSwap(false, true) {
		usecase.logf("sync skipped reason=already_running")
		return ErrSyncInProgress
	}
	defer usecase.syncRunning.Store(false)

	syncStartedAt := time.Now()
	effectiveConcurrency := usecase.concurrency
	if effectiveConcurrency <= 0 {
		effectiveConcurrency = 1
	}
	if effectiveConcurrency > len(sources) && len(sources) > 0 {
		effectiveConcurrency = len(sources)
	}

	usecase.logf("sync started regions=%d concurrency=%d force=%t", len(sources), effectiveConcurrency, force)

	regions := make([]string, 0, len(sources))
	for _, source := range sources {
		regions = append(regions, source.Region)
	}

	previousStatuses := usecase.loadStatusMap(ctx)

	var (
		resultMu      sync.Mutex
		syncErrors    []error
		failedRegions []string
		wg            sync.WaitGroup
	)

	recordFailure := func(region string, err error) {
		resultMu.Lock()
		syncErrors = append(syncErrors, err)
		failedRegions = append(failedRegions, region)
		resultMu.Unlock()
	}

	totalSteps := len(sources)
	semaphore := make(chan struct{}, effectiveConcurrency)
	for index, source := range sources {
		semaphore <- struct{}{}
		step := index + 1
		source := source

		wg.Go(func() {
			defer func() {
				<-semaphore
			}()

			regionCtx := ctx
			cancelRegion := func() {}
			if usecase.regionTimeout > 0 {
				regionCtx, cancelRegion = context.WithTimeout(ctx, usecase.regionTimeout)
			}
			defer cancelRegion()

			startedAt := time.Now().UTC()
			now := time.Now().UTC()
			resolvedCommit := ""
			previous, hasPreviousStatus := previousStatuses[source.Region]

			if !force && usecase.restoreFromLocalBackupWithoutStatus && !hasPreviousStatus {
				restored, restoreErr := usecase.restoreRegionFromLatestLocalBackup(regionCtx, source, step, totalSteps)
				if restoreErr != nil {
					usecase.logf("sync local bootstrap restore failed region=%s error=%v", source.Region, restoreErr)
				} else if restored {
					return
				}
			}

			if inspector, ok := usecase.cache.(MasterDataCacheIndexInspector); ok && !inspector.HasRegionIndex(source.Region) {
				pendingCommit := ""
				if hasPreviousStatus {
					pendingCommit = strings.TrimSpace(previous.SourceCommit)
				}

				if err := usecase.saveStatus(ctx, masterdata.SyncStatus{
					Region:         source.Region,
					Status:         "pending",
					FileCount:      0,
					SyncDurationMS: 0,
					LastSyncedAt:   now,
					SourceCommit:   pendingCommit,
					Source:         source,
					UpdatedAt:      now,
				}); err != nil {
					recordFailure(source.Region, fmt.Errorf("persist pending status for region %s: %w", source.Region, err))
				}
			}

			if resolver, ok := usecase.loader.(MasterDataSourceVersionResolver); ok {
				commit, resolveErr := resolver.ResolveRegionVersion(regionCtx, source)
				if resolveErr != nil {
					usecase.logf("sync compare failed region=%s error=%v", source.Region, resolveErr)
					message := "compare commit failed, fallback to full sync"
					if force {
						message = "resolve commit failed, continue with force sync"
					}
					usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
						Event:       "master_data_sync_progress",
						Status:      "running",
						Region:      source.Region,
						Phase:       "compare",
						Message:     message,
						CurrentStep: step,
						TotalSteps:  totalSteps,
						UpdatedAt:   now,
					})
				} else {
					resolvedCommit = strings.TrimSpace(commit)
					usecase.logf("sync compare region=%s remote_commit=%s force=%t", source.Region, resolvedCommit, force)
					if !force {
						if hasPreviousStatus && strings.EqualFold(strings.TrimSpace(previous.Status), "success") && previous.SourceCommit != "" && previous.SourceCommit == resolvedCommit {
							rebuildFromRedis := false
							if rebuilder, ok := usecase.cache.(MasterDataCacheIndexRebuilder); ok {
								rebuilt, rebuildErr := rebuilder.RebuildRegionIndexFromRedis(regionCtx, source.Region)
								if rebuildErr != nil {
									usecase.logf("sync compare region=%s commit=%s redis_index_rebuild=failed error=%v", source.Region, resolvedCommit, rebuildErr)
									usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
										Event:       "master_data_sync_progress",
										Status:      "running",
										Region:      source.Region,
										Phase:       "compare",
										Message:     "commit unchanged but redis index rebuild failed, fallback to full sync",
										CurrentStep: step,
										TotalSteps:  totalSteps,
										UpdatedAt:   now,
									})
								} else if rebuilt {
									rebuildFromRedis = true
									usecase.logf("sync skipped region=%s reason=commit_unchanged commit=%s index=rebuilt_from_redis", source.Region, resolvedCommit)
									usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
										Event:       "master_data_sync_progress",
										Status:      "success",
										Region:      source.Region,
										Phase:       "compare",
										Message:     "commit unchanged, rebuilt index from redis and skipped sync",
										CurrentStep: step,
										TotalSteps:  totalSteps,
										UpdatedAt:   now,
									})
								} else {
									usecase.logf("sync compare region=%s commit=%s redis_cache=empty fallback=full_sync", source.Region, resolvedCommit)
									usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
										Event:       "master_data_sync_progress",
										Status:      "running",
										Region:      source.Region,
										Phase:       "compare",
										Message:     "commit unchanged but redis cache missing, fallback to full sync",
										CurrentStep: step,
										TotalSteps:  totalSteps,
										UpdatedAt:   now,
									})
								}
							}

							if rebuildFromRedis {
								skippedAt := time.Now().UTC()
								if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
									Region:         previous.Region,
									Status:         previous.Status,
									FileCount:      previous.FileCount,
									SyncDurationMS: 0,
									LastSyncedAt:   previous.LastSyncedAt,
									SourceCommit:   resolvedCommit,
									ErrorMessage:   "",
									Source:         source,
									UpdatedAt:      skippedAt,
								}); statusErr != nil {
									recordFailure(source.Region, fmt.Errorf("persist unchanged status for region %s: %w", source.Region, statusErr))
								}
								return
							}

							if usecase.backupStore != nil {
								backupPayload, backupFound, backupErr := usecase.backupStore.LoadRegionPayload(regionCtx, source, resolvedCommit)
								if backupErr != nil {
									usecase.logf("sync compare region=%s commit=%s local_backup=load_failed error=%v", source.Region, resolvedCommit, backupErr)
									usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
										Event:       "master_data_sync_progress",
										Status:      "running",
										Region:      source.Region,
										Phase:       "compare",
										Message:     "commit unchanged but local backup read failed, fallback to full sync",
										CurrentStep: step,
										TotalSteps:  totalSteps,
										UpdatedAt:   now,
									})
								} else if backupFound {
									if cacheErr := usecase.cache.StoreRegion(regionCtx, source.Region, backupPayload); cacheErr != nil {
										usecase.logf("sync compare region=%s commit=%s local_backup=restore_failed error=%v", source.Region, resolvedCommit, cacheErr)
										usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
											Event:       "master_data_sync_progress",
											Status:      "running",
											Region:      source.Region,
											Phase:       "compare",
											Message:     "commit unchanged but local backup restore failed, fallback to full sync",
											CurrentStep: step,
											TotalSteps:  totalSteps,
											UpdatedAt:   now,
										})
									} else {
										usecase.logf("sync skipped region=%s reason=commit_unchanged commit=%s index=restored_from_local_backup", source.Region, resolvedCommit)
										usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
											Event:       "master_data_sync_progress",
											Status:      "success",
											Region:      source.Region,
											Phase:       "compare",
											Message:     "commit unchanged, restored cache from local backup and skipped sync",
											CurrentStep: step,
											TotalSteps:  totalSteps,
											UpdatedAt:   now,
										})

										skippedAt := time.Now().UTC()
										if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
											Region:         previous.Region,
											Status:         previous.Status,
											FileCount:      previous.FileCount,
											SyncDurationMS: 0,
											LastSyncedAt:   previous.LastSyncedAt,
											SourceCommit:   resolvedCommit,
											ErrorMessage:   "",
											Source:         source,
											UpdatedAt:      skippedAt,
										}); statusErr != nil {
											recordFailure(source.Region, fmt.Errorf("persist unchanged status for region %s: %w", source.Region, statusErr))
										}
										return
									}
								} else {
									usecase.logf("sync compare region=%s commit=%s local_backup=missing fallback=full_sync", source.Region, resolvedCommit)
								}
							}
						}
					}
				}
			}

			if err := usecase.saveStatus(ctx, masterdata.SyncStatus{
				Region:         source.Region,
				Status:         "running",
				FileCount:      0,
				SyncDurationMS: 0,
				LastSyncedAt:   now,
				SourceCommit:   resolvedCommit,
				Source:         source,
				UpdatedAt:      now,
			}); err != nil {
				usecase.logf("sync running status persist failed region=%s error=%v", source.Region, err)
			}

			usecase.logf(
				"sync progress step=%d/%d region=%s phase=load source=%s/%s ref=%s path=%s",
				step,
				totalSteps,
				source.Region,
				source.Owner,
				source.Repo,
				source.Ref,
				source.Path,
			)
			usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
				Event:       "master_data_sync_progress",
				Status:      "running",
				Region:      source.Region,
				Phase:       "load",
				Message:     "loading source files",
				CurrentStep: step,
				TotalSteps:  totalSteps,
				UpdatedAt:   now,
			})

			progressCtx := masterdata.WithProgressReporter(regionCtx, func(event masterdata.SyncUpdatedEvent) {
				if event.Event == "" {
					event.Event = "master_data_sync_progress"
				}
				if event.Status == "" {
					event.Status = "running"
				}
				if event.Region == "" {
					event.Region = source.Region
				}
				if event.CurrentStep == 0 {
					event.CurrentStep = step
				}
				if event.TotalSteps == 0 {
					event.TotalSteps = totalSteps
				}
				if event.UpdatedAt.IsZero() {
					event.UpdatedAt = time.Now().UTC()
				}

				usecase.publishSyncEvent(ctx, event)
			})

			loadSource := source
			if resolvedCommit != "" {
				loadSource.Ref = resolvedCommit
			}

			payload, err := usecase.loader.LoadRegion(progressCtx, loadSource)
			if err != nil {
				duration := time.Since(startedAt).Milliseconds()
				if isRateLimitError(err) {
					fallbackErr := usecase.fallbackToPreviousAvailableState(ctx, source, previous, now)
					if fallbackErr == nil {
						usecase.logf("sync rate limit fallback applied region=%s duration_ms=%d", source.Region, duration)
						usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
							Event:       "master_data_sync_progress",
							Status:      "success",
							Region:      source.Region,
							Phase:       "fallback",
							Message:     "rate limit reached, fallback to previous available state",
							CurrentStep: step,
							TotalSteps:  totalSteps,
							DurationMS:  duration,
							UpdatedAt:   time.Now().UTC(),
						})
						return
					}
					usecase.logf("sync rate limit fallback failed region=%s error=%v", source.Region, fallbackErr)
				}

				usecase.logf("sync failed region=%s phase=load duration_ms=%d error=%v", source.Region, duration, err)
				usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
					Event:       "master_data_sync_progress",
					Status:      "failed",
					Region:      source.Region,
					Phase:       "load",
					Message:     err.Error(),
					CurrentStep: step,
					TotalSteps:  totalSteps,
					DurationMS:  duration,
					UpdatedAt:   time.Now().UTC(),
				})
				recordFailure(source.Region, err)
				if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
					Region:         source.Region,
					Status:         "failed",
					FileCount:      0,
					SyncDurationMS: duration,
					LastSyncedAt:   now,
					SourceCommit:   resolvedCommit,
					ErrorMessage:   err.Error(),
					Source:         source,
					UpdatedAt:      now,
				}); statusErr != nil {
					recordFailure(source.Region, fmt.Errorf("persist failed status for region %s: %w", source.Region, statusErr))
				}
				return
			}

			usecase.logf(
				"sync progress step=%d/%d region=%s phase=cache files=%d",
				step,
				totalSteps,
				source.Region,
				len(payload),
			)
			usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
				Event:          "master_data_sync_progress",
				Status:         "running",
				Region:         source.Region,
				Phase:          "cache",
				Message:        "writing cache",
				CurrentStep:    step,
				TotalSteps:     totalSteps,
				FileCount:      len(payload),
				ProcessedFiles: 0,
				TotalFiles:     len(payload),
				UpdatedAt:      time.Now().UTC(),
			})

			if err := usecase.cache.StoreRegion(progressCtx, source.Region, payload); err != nil {
				duration := time.Since(startedAt).Milliseconds()
				usecase.logf("sync failed region=%s phase=cache files=%d duration_ms=%d error=%v", source.Region, len(payload), duration, err)
				usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
					Event:       "master_data_sync_progress",
					Status:      "failed",
					Region:      source.Region,
					Phase:       "cache",
					Message:     err.Error(),
					CurrentStep: step,
					TotalSteps:  totalSteps,
					FileCount:   len(payload),
					DurationMS:  duration,
					UpdatedAt:   time.Now().UTC(),
				})
				recordFailure(source.Region, err)
				if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
					Region:         source.Region,
					Status:         "failed",
					FileCount:      len(payload),
					SyncDurationMS: duration,
					LastSyncedAt:   now,
					SourceCommit:   resolvedCommit,
					ErrorMessage:   err.Error(),
					Source:         source,
					UpdatedAt:      now,
				}); statusErr != nil {
					recordFailure(source.Region, fmt.Errorf("persist failed status for region %s: %w", source.Region, statusErr))
				}
				return
			}

			if usecase.backupStore != nil {
				if backupErr := usecase.backupStore.SaveRegionPayload(regionCtx, source, resolvedCommit, payload); backupErr != nil {
					usecase.logf("sync backup save failed region=%s commit=%s error=%v", source.Region, resolvedCommit, backupErr)
				}
			}

			duration := time.Since(startedAt).Milliseconds()
			usecase.logf("sync success region=%s files=%d duration_ms=%d", source.Region, len(payload), duration)
			usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
				Event:       "master_data_sync_progress",
				Status:      "success",
				Region:      source.Region,
				Phase:       "done",
				Message:     "region sync completed",
				CurrentStep: step,
				TotalSteps:  totalSteps,
				FileCount:   len(payload),
				DurationMS:  duration,
				UpdatedAt:   time.Now().UTC(),
			})

			completedAt := time.Now().UTC()
			if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
				Region:         source.Region,
				Status:         "success",
				FileCount:      len(payload),
				SyncDurationMS: duration,
				LastSyncedAt:   completedAt,
				SourceCommit:   resolvedCommit,
				Source:         source,
				UpdatedAt:      completedAt,
			}); statusErr != nil {
				recordFailure(source.Region, fmt.Errorf("persist success status for region %s: %w", source.Region, statusErr))
			}
		})
	}

	wg.Wait()

	status := "success"
	if len(syncErrors) > 0 {
		status = "failed"
	}

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:         "master_data_updated",
		Status:        status,
		Regions:       regions,
		FailedRegions: failedRegions,
		UpdatedAt:     time.Now().UTC(),
	})

	usecase.logf(
		"sync completed status=%s regions=%d failed_regions=%d duration_ms=%d",
		status,
		len(regions),
		len(failedRegions),
		time.Since(syncStartedAt).Milliseconds(),
	)

	if len(syncErrors) > 0 {
		return errors.Join(syncErrors...)
	}

	return nil
}

func (usecase *MasterDataSyncUsecase) loadStatusMap(ctx context.Context) map[string]masterdata.SyncStatus {
	statusMap := make(map[string]masterdata.SyncStatus)
	if usecase.statusStore == nil {
		return statusMap
	}

	statuses, err := usecase.statusStore.List(ctx)
	if err != nil {
		usecase.logf("load previous statuses failed error=%v", err)
		return statusMap
	}

	for _, status := range statuses {
		if strings.TrimSpace(status.Region) == "" {
			continue
		}
		statusMap[status.Region] = status
	}

	if successStore, ok := usecase.statusStore.(MasterDataSyncLatestSuccessStore); ok {
		successStatuses, successErr := successStore.ListLatestSuccess(ctx)
		if successErr != nil {
			usecase.logf("load latest successful statuses failed error=%v", successErr)
			return statusMap
		}

		for _, status := range successStatuses {
			if strings.TrimSpace(status.Region) == "" {
				continue
			}
			statusMap[status.Region] = status
		}
	}

	return statusMap
}

func (usecase *MasterDataSyncUsecase) Status(ctx context.Context) ([]masterdata.SyncStatus, error) {
	if usecase.statusStore == nil {
		return nil, nil
	}

	return usecase.statusStore.List(ctx)
}

func (usecase *MasterDataSyncUsecase) DashboardStatus(ctx context.Context) ([]masterdata.SyncStatus, error) {
	statuses, err := usecase.Status(ctx)
	if err != nil {
		return nil, err
	}
	if usecase.IsSyncRunning() || usecase.statusStore == nil {
		return statuses, nil
	}

	hasRunningStatus := false
	for _, status := range statuses {
		if strings.EqualFold(strings.TrimSpace(status.Status), "running") {
			hasRunningStatus = true
			break
		}
	}
	if !hasRunningStatus {
		return statuses, nil
	}

	stableStore, ok := usecase.statusStore.(MasterDataSyncLatestStableStore)
	if !ok {
		return statuses, nil
	}

	stableStatuses, err := stableStore.ListLatestStable(ctx)
	if err != nil {
		usecase.logf("dashboard status fallback skipped error=%v", err)
		return statuses, nil
	}
	if len(stableStatuses) == 0 {
		return statuses, nil
	}

	stableByRegion := make(map[string]masterdata.SyncStatus, len(stableStatuses))
	for _, status := range stableStatuses {
		stableByRegion[strings.ToLower(strings.TrimSpace(status.Region))] = status
	}

	merged := make([]masterdata.SyncStatus, 0, len(statuses))
	for _, status := range statuses {
		if !strings.EqualFold(strings.TrimSpace(status.Status), "running") {
			merged = append(merged, status)
			continue
		}

		if stableStatus, ok := stableByRegion[strings.ToLower(strings.TrimSpace(status.Region))]; ok {
			merged = append(merged, stableStatus)
			continue
		}

		merged = append(merged, status)
	}

	return merged, nil
}

func (usecase *MasterDataSyncUsecase) ConfiguredRegions() []string {
	regions := make([]string, 0, len(usecase.sources))
	for _, source := range usecase.sources {
		region := strings.ToLower(strings.TrimSpace(source.Region))
		if region == "" {
			continue
		}
		regions = append(regions, region)
	}

	sort.Strings(regions)
	return regions
}

func (usecase *MasterDataSyncUsecase) WarmConfiguredRegionIndexes(ctx context.Context) ([]string, error) {
	loader, ok := usecase.cache.(MasterDataCacheIndexLoader)
	if !ok {
		return nil, nil
	}

	regions := usecase.ConfiguredRegions()
	warmed := make([]string, 0, len(regions))
	for _, region := range regions {
		loaded, err := loader.LoadRegionIndexFromRedis(ctx, region)
		if err != nil {
			return warmed, fmt.Errorf("warm region index %s: %w", region, err)
		}
		if loaded {
			warmed = append(warmed, region)
		}
	}

	return warmed, nil
}

func (usecase *MasterDataSyncUsecase) EnsureConfiguredRegionIndexes(ctx context.Context) ([]string, []string, error) {
	regions := usecase.ConfiguredRegions()
	if len(regions) == 0 {
		return nil, nil, nil
	}

	loader, canLoad := usecase.cache.(MasterDataCacheIndexLoader)
	rebuilder, canRebuild := usecase.cache.(MasterDataCacheIndexRebuilder)
	inspector, canInspect := usecase.cache.(MasterDataCacheIndexInspector)
	if !canLoad && !canRebuild {
		return nil, nil, nil
	}

	loadedRegions := make([]string, 0, len(regions))
	rebuiltRegions := make([]string, 0, len(regions))
	for _, region := range regions {
		if canInspect && inspector.HasRegionIndex(region) {
			continue
		}

		if canLoad {
			loaded, err := loader.LoadRegionIndexFromRedis(ctx, region)
			if err != nil {
				return loadedRegions, rebuiltRegions, fmt.Errorf("ensure region index %s load: %w", region, err)
			}
			if loaded {
				loadedRegions = append(loadedRegions, region)
				continue
			}
		}

		if canRebuild {
			rebuilt, err := rebuilder.RebuildRegionIndexFromRedis(ctx, region)
			if err != nil {
				return loadedRegions, rebuiltRegions, fmt.Errorf("ensure region index %s rebuild: %w", region, err)
			}
			if rebuilt {
				rebuiltRegions = append(rebuiltRegions, region)
			}
		}
	}

	return loadedRegions, rebuiltRegions, nil
}

func (usecase *MasterDataSyncUsecase) IsSyncRunning() bool {
	return usecase.syncRunning.Load()
}

func (usecase *MasterDataSyncUsecase) GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	if usecase.cache == nil {
		return nil, false, nil
	}

	return usecase.cache.GetByID(ctx, region, entity, id)
}

func (usecase *MasterDataSyncUsecase) ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	if usecase.cache == nil {
		return []map[string]any{}, 0, nil
	}

	return usecase.cache.ListByPage(ctx, region, entity, page, pageSize)
}

func (usecase *MasterDataSyncUsecase) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	if usecase.cache == nil {
		return []map[string]any{}, nil
	}

	return usecase.cache.ListAll(ctx, region, entity)
}

func (usecase *MasterDataSyncUsecase) Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	if usecase.cache == nil {
		return []masterdata.SearchMatch{}, nil
	}

	return usecase.cache.Search(ctx, region, entity, query, fields, limit)
}

func (usecase *MasterDataSyncUsecase) CurrentEvent(ctx context.Context, region string, now time.Time) (map[string]any, bool, error) {
	if usecase.cache == nil {
		return nil, false, nil
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	if normalizedRegion == "" {
		return nil, false, nil
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowMillis := now.UnixMilli()

	cached, found, err := usecase.readCurrentEventCache(ctx, normalizedRegion)
	if err != nil {
		return nil, false, err
	}
	if found && isEventInTimeRange(cached, nowMillis) {
		return cached, true, nil
	}

	regionLock := usecase.currentEventLock(normalizedRegion)
	regionLock.Lock()
	defer regionLock.Unlock()

	cached, found, err = usecase.readCurrentEventCache(ctx, normalizedRegion)
	if err != nil {
		return nil, false, err
	}
	if found && isEventInTimeRange(cached, nowMillis) {
		return cached, true, nil
	}

	current, found, err := usecase.findCurrentEvent(ctx, normalizedRegion, nowMillis)
	if err != nil {
		return nil, false, err
	}

	payload := map[string]any{"currentEvents.json": []any{}}
	if found {
		payload["currentEvents.json"] = []any{current}
	}

	if err := usecase.cache.StoreRegion(ctx, normalizedRegion, payload); err != nil {
		return nil, false, fmt.Errorf("store current event cache region %s: %w", normalizedRegion, err)
	}

	if !found {
		return nil, false, nil
	}

	return current, true, nil
}

func (usecase *MasterDataSyncUsecase) restoreRegionFromLatestLocalBackup(ctx context.Context, source masterdata.Source, currentStep int, totalSteps int) (bool, error) {
	if usecase.backupStore == nil {
		return false, nil
	}

	backupPayload, commit, restoredAt, found, err := usecase.backupStore.LoadLatestRegionPayload(ctx, source)
	if err != nil {
		return false, fmt.Errorf("load latest backup payload: %w", err)
	}
	if !found {
		return false, nil
	}

	if err := usecase.cache.StoreRegion(ctx, source.Region, backupPayload); err != nil {
		return false, fmt.Errorf("restore latest backup payload: %w", err)
	}

	if restoredAt.IsZero() {
		restoredAt = time.Now().UTC()
	}

	statusSavedAt := time.Now().UTC()
	status := masterdata.SyncStatus{
		Region:         source.Region,
		Status:         "success",
		FileCount:      len(backupPayload),
		SyncDurationMS: 0,
		LastSyncedAt:   restoredAt,
		SourceCommit:   strings.TrimSpace(commit),
		Source:         source,
		UpdatedAt:      statusSavedAt,
	}
	if err := usecase.saveStatus(ctx, status); err != nil {
		return false, fmt.Errorf("save restored backup status: %w", err)
	}

	usecase.logf("sync bootstrap restored from local backup region=%s commit=%s files=%d", source.Region, status.SourceCommit, len(backupPayload))
	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "success",
		Region:      source.Region,
		Phase:       "bootstrap",
		Message:     "database status missing, restored cache from local backup",
		CurrentStep: currentStep,
		TotalSteps:  totalSteps,
		FileCount:   len(backupPayload),
		UpdatedAt:   statusSavedAt,
	})

	return true, nil
}

func (usecase *MasterDataSyncUsecase) fallbackToPreviousAvailableState(ctx context.Context, source masterdata.Source, previous masterdata.SyncStatus, fallbackAt time.Time) error {
	if !strings.EqualFold(strings.TrimSpace(previous.Status), "success") {
		return errors.New("previous available status not found")
	}

	restored := false
	commit := strings.TrimSpace(previous.SourceCommit)
	if usecase.backupStore != nil && commit != "" {
		backupPayload, backupFound, backupErr := usecase.backupStore.LoadRegionPayload(ctx, source, commit)
		if backupErr != nil {
			return fmt.Errorf("load backup payload: %w", backupErr)
		}
		if backupFound {
			if err := usecase.cache.StoreRegion(ctx, source.Region, backupPayload); err != nil {
				return fmt.Errorf("restore backup payload: %w", err)
			}
			restored = true
		}
	}

	if fallbackAt.IsZero() {
		fallbackAt = time.Now().UTC()
	}

	status := masterdata.SyncStatus{
		Region:         source.Region,
		Status:         "success",
		FileCount:      previous.FileCount,
		SyncDurationMS: 0,
		LastSyncedAt:   previous.LastSyncedAt,
		SourceCommit:   commit,
		ErrorMessage:   "",
		Source:         source,
		UpdatedAt:      fallbackAt,
	}

	if err := usecase.saveStatus(ctx, status); err != nil {
		return fmt.Errorf("save fallback status: %w", err)
	}

	if restored {
		usecase.logf("fallback restored from backup region=%s commit=%s", source.Region, commit)
	} else {
		usecase.logf("fallback kept previous cached state region=%s commit=%s", source.Region, commit)
	}

	return nil
}

func (usecase *MasterDataSyncUsecase) currentEventLock(region string) *sync.Mutex {
	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	if normalizedRegion == "" {
		normalizedRegion = "default"
	}

	if existing, ok := usecase.currentEventLocks.Load(normalizedRegion); ok {
		if lock, ok := existing.(*sync.Mutex); ok {
			return lock
		}
	}

	newLock := &sync.Mutex{}
	actual, _ := usecase.currentEventLocks.LoadOrStore(normalizedRegion, newLock)
	lock, ok := actual.(*sync.Mutex)
	if ok {
		return lock
	}

	return newLock
}

func (usecase *MasterDataSyncUsecase) readCurrentEventCache(ctx context.Context, region string) (map[string]any, bool, error) {
	records, _, err := usecase.cache.ListByPage(ctx, region, "currentevents", 1, 1)
	if err != nil {
		return nil, false, fmt.Errorf("read current event cache region %s: %w", region, err)
	}
	if len(records) == 0 {
		return nil, false, nil
	}

	return records[0], true, nil
}

func (usecase *MasterDataSyncUsecase) findCurrentEvent(ctx context.Context, region string, nowMillis int64) (map[string]any, bool, error) {
	page := 1
	pageSize := 100
	selectedStartAt := int64(0)
	var selected map[string]any

	for {
		records, _, err := usecase.cache.ListByPage(ctx, region, "events", page, pageSize)
		if err != nil {
			return nil, false, fmt.Errorf("list events region %s page %d: %w", region, page, err)
		}
		if len(records) == 0 {
			break
		}

		for _, record := range records {
			startAt, endAt, ok := resolveEventTimeRange(record)
			if !ok {
				continue
			}

			if nowMillis < startAt || nowMillis > endAt {
				continue
			}

			if selected == nil || startAt > selectedStartAt {
				selected = record
				selectedStartAt = startAt
			}
		}

		page++
	}

	if selected == nil {
		return nil, false, nil
	}

	return selected, true, nil
}

func isEventInTimeRange(record map[string]any, nowMillis int64) bool {
	startAt, endAt, ok := resolveEventTimeRange(record)
	if !ok {
		return false
	}

	return nowMillis >= startAt && nowMillis <= endAt
}

func resolveEventTimeRange(record map[string]any) (int64, int64, bool) {
	if record == nil {
		return 0, 0, false
	}

	starts := collectPositiveTimestamps(record, "startAt", "eventOnlyComponentDisplayStartAt", "distributionStartAt")
	if len(starts) == 0 {
		return 0, 0, false
	}
	ends := collectPositiveTimestamps(record, "closedAt", "aggregateAt", "distributionEndAt", "eventOnlyComponentDisplayEndAt")
	if len(ends) == 0 {
		return 0, 0, false
	}

	startAt := starts[0]
	for _, value := range starts[1:] {
		if value < startAt {
			startAt = value
		}
	}

	endAt := ends[0]
	for _, value := range ends[1:] {
		if value > endAt {
			endAt = value
		}
	}

	if endAt < startAt {
		return 0, 0, false
	}

	return startAt, endAt, true
}

func collectPositiveTimestamps(record map[string]any, keys ...string) []int64 {
	values := make([]int64, 0, len(keys))
	for _, key := range keys {
		value, exists := record[key]
		if !exists {
			continue
		}

		timestamp, ok := parseTimestamp(value)
		if !ok || timestamp <= 0 {
			continue
		}

		values = append(values, timestamp)
	}

	return values
}

func parseTimestamp(value any) (int64, bool) {
	toMillis := func(timestamp int64) int64 {
		return normalizeEpochTimestamp(timestamp)
	}

	switch typed := value.(type) {
	case int64:
		return toMillis(typed), true
	case int:
		return toMillis(int64(typed)), true
	case int32:
		return toMillis(int64(typed)), true
	case float64:
		return toMillis(int64(typed)), true
	case float32:
		return toMillis(int64(typed)), true
	case uint64:
		return toMillis(int64(typed)), true
	case uint:
		return toMillis(int64(typed)), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return toMillis(parsed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			parsedFloat, parseFloatErr := strconv.ParseFloat(trimmed, 64)
			if parseFloatErr == nil {
				return toMillis(int64(parsedFloat)), true
			}

			parsedTime, parseTimeErr := time.Parse(time.RFC3339Nano, trimmed)
			if parseTimeErr != nil {
				parsedTime, parseTimeErr = time.Parse(time.RFC3339, trimmed)
				if parseTimeErr != nil {
					for _, layout := range []string{
						"2006-01-02 15:04:05",
						"2006-01-02 15:04:05Z07:00",
						"2006-01-02 15:04:05 -0700",
					} {
						parsedTime, parseTimeErr = time.Parse(layout, trimmed)
						if parseTimeErr == nil {
							return parsedTime.UnixMilli(), true
						}
					}

					return 0, false
				}
			}

			return parsedTime.UnixMilli(), true
		}
		return toMillis(parsed), true
	default:
		return 0, false
	}
}

func normalizeEpochTimestamp(timestamp int64) int64 {
	abs := timestamp
	if abs < 0 {
		abs = -abs
	}

	switch {
	case abs == 0:
		return 0
	case abs < 100_000_000_000:
		return timestamp * 1000
	case abs >= 100_000_000_000_000_000:
		return timestamp / 1_000_000
	case abs >= 100_000_000_000_000:
		return timestamp / 1000
	default:
		return timestamp
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}

	keywords := []string{
		"rate limit",
		"rate-limit",
		"api rate limit exceeded",
		"too many requests",
		"status code 429",
		"http 429",
	}

	for _, keyword := range keywords {
		if strings.Contains(message, keyword) {
			return true
		}
	}

	return false
}

func (usecase *MasterDataSyncUsecase) saveStatus(ctx context.Context, status masterdata.SyncStatus) error {
	if usecase.statusStore == nil {
		usecase.logf("sync status skipped region=%s reason=status_store_disabled", status.Region)
		return nil
	}

	usecase.statusMu.Lock()
	defer usecase.statusMu.Unlock()

	err := usecase.statusStore.Save(ctx, status)
	if err != nil && ctx.Err() != nil {
		retryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		retryErr := usecase.statusStore.Save(retryCtx, status)
		if retryErr == nil {
			usecase.logf("sync status save recovered by retry region=%s status=%s", status.Region, status.Status)
			err = nil
		} else {
			err = fmt.Errorf("%w; retry failed: %v", err, retryErr)
		}
	}
	if err != nil {
		usecase.logf("sync status save failed region=%s status=%s error=%v", status.Region, status.Status, err)
		status.ErrorMessage = strings.TrimSpace(status.ErrorMessage + "; failed to persist status: " + err.Error())
		return err
	}

	usecase.logf(
		"sync status saved region=%s status=%s files=%d duration_ms=%d",
		status.Region,
		status.Status,
		status.FileCount,
		status.SyncDurationMS,
	)

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:      "master_data_status",
		Status:     status.Status,
		Region:     status.Region,
		StatusItem: &status,
		UpdatedAt:  status.UpdatedAt,
	})

	return nil
}

func (usecase *MasterDataSyncUsecase) publishSyncEvent(ctx context.Context, event masterdata.SyncUpdatedEvent) {
	usecase.logSyncEventDebug(event)

	if usecase.publisher == nil {
		usecase.logf("sync event skipped reason=publisher_disabled")
		return
	}

	if err := usecase.publisher.PublishMasterDataUpdated(ctx, event); err != nil {
		usecase.logf("sync event publish failed event=%s status=%s error=%v", event.Event, event.Status, err)
		return
	}

	if event.Event == "master_data_updated" {
		usecase.logf(
			"sync event published event=%s status=%s regions=%d failed_regions=%d",
			event.Event,
			event.Status,
			len(event.Regions),
			len(event.FailedRegions),
		)
	}
}

func (usecase *MasterDataSyncUsecase) logSyncEventDebug(event masterdata.SyncUpdatedEvent) {
	fields := []any{
		"component", masterDataSyncLogComponent,
		"event", strings.TrimSpace(event.Event),
		"status", strings.TrimSpace(event.Status),
	}

	if region := strings.TrimSpace(event.Region); region != "" {
		fields = append(fields, "region", region)
	}
	if phase := strings.TrimSpace(event.Phase); phase != "" {
		fields = append(fields, "phase", phase)
	}
	if message := strings.TrimSpace(event.Message); message != "" {
		fields = append(fields, "message", message)
	}
	if filePath := strings.TrimSpace(event.FilePath); filePath != "" {
		fields = append(fields, "file_path", filePath)
	}
	if event.CurrentStep > 0 {
		fields = append(fields, "current_step", event.CurrentStep)
	}
	if event.TotalSteps > 0 {
		fields = append(fields, "total_steps", event.TotalSteps)
	}
	if event.FileCount > 0 {
		fields = append(fields, "file_count", event.FileCount)
	}
	if event.ProcessedFiles > 0 {
		fields = append(fields, "processed_files", event.ProcessedFiles)
	}
	if event.TotalFiles > 0 {
		fields = append(fields, "total_files", event.TotalFiles)
	}
	if event.FailedFiles > 0 {
		fields = append(fields, "failed_files", event.FailedFiles)
	}
	if event.DurationMS > 0 {
		fields = append(fields, "duration_ms", event.DurationMS)
	}
	if len(event.Regions) > 0 {
		fields = append(fields, "regions", append([]string(nil), event.Regions...))
	}
	if len(event.FailedRegions) > 0 {
		fields = append(fields, "failed_regions", append([]string(nil), event.FailedRegions...))
	}
	if !event.UpdatedAt.IsZero() {
		fields = append(fields, "updated_at", event.UpdatedAt)
	}
	if event.StatusItem != nil {
		fields = append(
			fields,
			"status_item_region", strings.TrimSpace(event.StatusItem.Region),
			"status_item_status", strings.TrimSpace(event.StatusItem.Status),
			"status_item_file_count", event.StatusItem.FileCount,
			"status_item_duration_ms", event.StatusItem.SyncDurationMS,
			"status_item_source_commit", strings.TrimSpace(event.StatusItem.SourceCommit),
		)
		if errorMessage := strings.TrimSpace(event.StatusItem.ErrorMessage); errorMessage != "" {
			fields = append(fields, "status_item_error", errorMessage)
		}
	}

	zap.S().Debugw("master data sync event", fields...)
}

func (usecase *MasterDataSyncUsecase) logf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	lowerMessage := strings.ToLower(message)

	switch {
	case strings.Contains(lowerMessage, "status=failed") ||
		strings.Contains(lowerMessage, " failed:") ||
		strings.Contains(lowerMessage, "failed:") ||
		strings.Contains(lowerMessage, " error=") ||
		strings.Contains(lowerMessage, "with errors"):
		logging.ErrorKV(masterDataSyncLogComponent, message)
	case strings.Contains(lowerMessage, "completed") || strings.Contains(lowerMessage, "success") || strings.Contains(lowerMessage, "skipped"):
		logging.InfoKV(masterDataSyncLogComponent, message)
	default:
		logging.DebugKV(masterDataSyncLogComponent, message)
	}
}

func BuildMasterDataSources(cfgSources map[string]struct {
	Region string
	Owner  string
	Repo   string
	Ref    string
	Path   string
}) []masterdata.Source {
	sources := make([]masterdata.Source, 0, len(cfgSources))
	for region, source := range cfgSources {
		normalizedRegion := strings.ToLower(strings.TrimSpace(region))
		if normalizedRegion == "" {
			continue
		}

		sources = append(sources, masterdata.Source{
			Region: normalizedRegion,
			Owner:  source.Owner,
			Repo:   source.Repo,
			Ref:    source.Ref,
			Path:   source.Path,
		})
	}

	return sources
}

func ValidateMasterDataSources(sources []masterdata.Source) error {
	for _, source := range sources {
		if strings.TrimSpace(source.Region) == "" {
			return fmt.Errorf("master data source region is required")
		}
		if strings.TrimSpace(source.Owner) == "" {
			return fmt.Errorf("master data source owner is required for region %s", source.Region)
		}
		if strings.TrimSpace(source.Repo) == "" {
			return fmt.Errorf("master data source repo is required for region %s", source.Region)
		}
		if strings.TrimSpace(source.Ref) == "" {
			return fmt.Errorf("master data source ref is required for region %s", source.Region)
		}
	}

	return nil
}
