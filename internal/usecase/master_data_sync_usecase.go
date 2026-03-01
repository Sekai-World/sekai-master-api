package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sekai-master-api/internal/domain/masterdata"
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
	ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error)
	Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error)
}

type MasterDataSyncStatusStore interface {
	Save(ctx context.Context, status masterdata.SyncStatus) error
	List(ctx context.Context) ([]masterdata.SyncStatus, error)
}

type MasterDataEventPublisher interface {
	PublishMasterDataUpdated(ctx context.Context, event masterdata.SyncUpdatedEvent) error
}

type MasterDataSyncUsecase struct {
	sources     []masterdata.Source
	loader      MasterDataSourceLoader
	cache       MasterDataCache
	statusStore MasterDataSyncStatusStore
	publisher   MasterDataEventPublisher
	concurrency int
	statusMu    sync.Mutex
	syncRunning atomic.Bool
}

const masterDataSyncLogComponent = "master-data-sync"

var ErrSyncInProgress = errors.New("master data sync is already running")

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
		concurrency: concurrency,
	}
}

func (usecase *MasterDataSyncUsecase) SyncAll(ctx context.Context) error {
	return usecase.syncAll(ctx, false)
}

func (usecase *MasterDataSyncUsecase) SyncAllForce(ctx context.Context) error {
	return usecase.syncAll(ctx, true)
}

func (usecase *MasterDataSyncUsecase) syncAll(ctx context.Context, force bool) error {
	if !usecase.syncRunning.CompareAndSwap(false, true) {
		usecase.logf("sync skipped: reason=already_running")
		return ErrSyncInProgress
	}
	defer usecase.syncRunning.Store(false)

	syncStartedAt := time.Now()
	effectiveConcurrency := usecase.concurrency
	if effectiveConcurrency <= 0 {
		effectiveConcurrency = 1
	}
	if effectiveConcurrency > len(usecase.sources) && len(usecase.sources) > 0 {
		effectiveConcurrency = len(usecase.sources)
	}

	usecase.logf("sync started: regions=%d concurrency=%d force=%t", len(usecase.sources), effectiveConcurrency, force)

	regions := make([]string, 0, len(usecase.sources))
	for _, source := range usecase.sources {
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

	totalSteps := len(usecase.sources)
	semaphore := make(chan struct{}, effectiveConcurrency)
	for index, source := range usecase.sources {
		semaphore <- struct{}{}
		step := index + 1
		source := source

		wg.Go(func() {
			defer func() {
				<-semaphore
			}()

			startedAt := time.Now().UTC()
			now := time.Now().UTC()
			resolvedCommit := ""

			if resolver, ok := usecase.loader.(MasterDataSourceVersionResolver); ok {
				commit, resolveErr := resolver.ResolveRegionVersion(ctx, source)
				if resolveErr != nil {
					usecase.logf("sync compare failed: region=%s error=%v", source.Region, resolveErr)
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
					usecase.logf("sync compare: region=%s remote_commit=%s force=%t", source.Region, resolvedCommit, force)
					if !force {
						if previous, exists := previousStatuses[source.Region]; exists && previous.SourceCommit != "" && previous.SourceCommit == resolvedCommit {
							usecase.logf("sync skipped: region=%s reason=commit_unchanged commit=%s", source.Region, resolvedCommit)
							usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
								Event:       "master_data_sync_progress",
								Status:      "success",
								Region:      source.Region,
								Phase:       "compare",
								Message:     "commit unchanged, skip sync",
								CurrentStep: step,
								TotalSteps:  totalSteps,
								UpdatedAt:   now,
							})

							if statusErr := usecase.saveStatus(ctx, masterdata.SyncStatus{
								Region:         previous.Region,
								Status:         previous.Status,
								FileCount:      previous.FileCount,
								SyncDurationMS: 0,
								LastSyncedAt:   previous.LastSyncedAt,
								SourceCommit:   resolvedCommit,
								ErrorMessage:   "",
								Source:         source,
								UpdatedAt:      now,
							}); statusErr != nil {
								recordFailure(source.Region, fmt.Errorf("persist unchanged status for region %s: %w", source.Region, statusErr))
							}
							return
						}
					}
				}
			}

			if err := usecase.saveStatus(ctx, masterdata.SyncStatus{
				Region:         source.Region,
				Status:         "pending",
				FileCount:      0,
				SyncDurationMS: 0,
				LastSyncedAt:   now,
				SourceCommit:   resolvedCommit,
				Source:         source,
				UpdatedAt:      now,
			}); err != nil {
				usecase.logf("sync pending status persist failed: region=%s error=%v", source.Region, err)
			}

			usecase.logf(
				"sync progress: step=%d/%d region=%s phase=load source=%s/%s ref=%s path=%s",
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

			progressCtx := masterdata.WithProgressReporter(ctx, func(event masterdata.SyncUpdatedEvent) {
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

			payload, err := usecase.loader.LoadRegion(progressCtx, source)
			if err != nil {
				duration := time.Since(startedAt).Milliseconds()
				usecase.logf("sync failed: region=%s phase=load duration_ms=%d error=%v", source.Region, duration, err)
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
				"sync progress: step=%d/%d region=%s phase=cache files=%d",
				step,
				totalSteps,
				source.Region,
				len(payload),
			)
			usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
				Event:       "master_data_sync_progress",
				Status:      "running",
				Region:      source.Region,
				Phase:       "cache",
				Message:     "writing cache",
				CurrentStep: step,
				TotalSteps:  totalSteps,
				FileCount:   len(payload),
				UpdatedAt:   time.Now().UTC(),
			})

			if err := usecase.cache.StoreRegion(ctx, source.Region, payload); err != nil {
				duration := time.Since(startedAt).Milliseconds()
				usecase.logf("sync failed: region=%s phase=cache files=%d duration_ms=%d error=%v", source.Region, len(payload), duration, err)
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

			duration := time.Since(startedAt).Milliseconds()
			usecase.logf("sync success: region=%s files=%d duration_ms=%d", source.Region, len(payload), duration)
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
		"sync completed: status=%s regions=%d failed_regions=%d duration_ms=%d",
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
		usecase.logf("load previous statuses failed: error=%v", err)
		return statusMap
	}

	for _, status := range statuses {
		if strings.TrimSpace(status.Region) == "" {
			continue
		}
		statusMap[status.Region] = status
	}

	return statusMap
}

func (usecase *MasterDataSyncUsecase) Status(ctx context.Context) ([]masterdata.SyncStatus, error) {
	if usecase.statusStore == nil {
		return nil, nil
	}

	return usecase.statusStore.List(ctx)
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

func (usecase *MasterDataSyncUsecase) Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	if usecase.cache == nil {
		return []masterdata.SearchMatch{}, nil
	}

	return usecase.cache.Search(ctx, region, entity, query, fields, limit)
}

func (usecase *MasterDataSyncUsecase) saveStatus(ctx context.Context, status masterdata.SyncStatus) error {
	if usecase.statusStore == nil {
		usecase.logf("sync status skipped: region=%s reason=status_store_disabled", status.Region)
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
			usecase.logf("sync status save recovered by retry: region=%s status=%s", status.Region, status.Status)
			err = nil
		} else {
			err = fmt.Errorf("%w; retry failed: %v", err, retryErr)
		}
	}
	if err != nil {
		usecase.logf("sync status save failed: region=%s status=%s error=%v", status.Region, status.Status, err)
		status.ErrorMessage = strings.TrimSpace(status.ErrorMessage + "; failed to persist status: " + err.Error())
		return err
	}

	usecase.logf(
		"sync status saved: region=%s status=%s files=%d duration_ms=%d",
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
	if usecase.publisher == nil {
		usecase.logf("sync event skipped: reason=publisher_disabled")
		return
	}

	if err := usecase.publisher.PublishMasterDataUpdated(ctx, event); err != nil {
		usecase.logf("sync event publish failed: event=%s status=%s error=%v", event.Event, event.Status, err)
		return
	}

	if event.Event == "master_data_updated" {
		usecase.logf(
			"sync event published: event=%s status=%s regions=%d failed_regions=%d",
			event.Event,
			event.Status,
			len(event.Regions),
			len(event.FailedRegions),
		)
	}
}

func (usecase *MasterDataSyncUsecase) logf(format string, args ...any) {
	log.Printf("component=%s %s", masterDataSyncLogComponent, fmt.Sprintf(format, args...))
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
