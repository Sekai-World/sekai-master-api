package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/tracing"
)

type MasterDataSourceLoader interface {
	LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error)
}

type MasterDataSourceVersionResolver interface {
	ResolveRegionVersion(ctx context.Context, source masterdata.Source) (string, error)
}

type MasterDataSourceVersionManifestLoader interface {
	LoadVersionManifest(ctx context.Context, source masterdata.Source) (map[string]any, bool, error)
}

type MasterDataCache interface {
	StoreRegion(ctx context.Context, region string, payload map[string]any) error
	GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)
	ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error)
	ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error)
	Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error)
}

type MasterDataCacheSourceDigestStorer interface {
	StoreRegionWithSourceDigests(ctx context.Context, region string, payload map[string]any, fileDigests map[string]string) error
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

type MasterDataCacheEntityInspector interface {
	HasEntityRecords(ctx context.Context, region string, entity string) (bool, error)
}

type MasterDataCacheVersionStorer interface {
	StoreRegionVersionPayload(ctx context.Context, region string, version any) error
}

type MasterDataCacheVersionLoader interface {
	LoadRegionVersionPayload(ctx context.Context, region string) (any, bool, error)
}

// MasterDataRedisPinger reports whether the Redis-backed cache is reachable. It
// is satisfied by *storage.RedisMasterDataCache and lets the readiness probe
// verify Redis connectivity without depending on the concrete storage type.
type MasterDataRedisPinger interface {
	Ping(ctx context.Context) error
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

// MasterDataSyncLeaseCoordinator is the cross-pod sync ownership contract
// (docs/distributed-sync-coordination.md). Acquire returns
// masterdata.ErrSyncLeaseHeld when another unexpired owner holds the lease;
// Renew returns masterdata.ErrSyncLeaseLost after ownership moved elsewhere.
// A nil coordinator (the default) keeps the historical process-local-only
// behavior.
type MasterDataSyncLeaseCoordinator interface {
	Acquire(ctx context.Context) (masterdata.SyncLeaseClaim, error)
	Renew(ctx context.Context, claim masterdata.SyncLeaseClaim) error
	Release(ctx context.Context, claim masterdata.SyncLeaseClaim) error
	State(ctx context.Context) (masterdata.SyncLeaseState, error)
}

type MasterDataPayloadBackupStore interface {
	SaveRegionPayload(ctx context.Context, source masterdata.Source, commit string, payload map[string]any) error
	LoadRegionPayload(ctx context.Context, source masterdata.Source, commit string) (map[string]any, bool, error)
	LoadLatestRegionPayload(ctx context.Context, source masterdata.Source) (map[string]any, string, time.Time, bool, error)
}

type MasterDataVersionBackupStore interface {
	LoadLatestRegionVersionPayload(ctx context.Context, source masterdata.Source) (any, string, time.Time, bool, error)
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
	jobTimeout                          time.Duration
	restoreFromLocalBackupWithoutStatus bool
	statusMu                            sync.Mutex
	syncRunning                         atomic.Bool

	// leaseCoordinator owns cross-pod sync admission; nil keeps the
	// process-local-only behavior. currentLeaseToken carries the fencing
	// token of the lease held by this process's running job and is stamped
	// onto every status write so the fenced status store can reject stale
	// owners after a takeover.
	leaseCoordinator      MasterDataSyncLeaseCoordinator
	leaseHeartbeatTimeout time.Duration
	leaseReleaseTimeout   time.Duration
	currentLeaseToken     atomic.Int64

	currentEventLocks sync.Map

	// lifecycleDone carries the done channel of the application lifecycle
	// context. When set, it cancels long-running background sync workers (admin
	// StartSync, webhook SyncRegion) during graceful shutdown. It is nil by
	// default, which disables lifecycle cancellation. We store the done channel
	// instead of the context.Context itself so the struct never retains a context
	// (godre/S8242).
	lifecycleDone <-chan struct{}
	// syncWG tracks lifecycle-managed sync workers so the app can wait for
	// them to stop before tearing down dependencies on graceful shutdown.
	syncWG sync.WaitGroup

	// admitMu guards admissionClosed together with the syncWG Add so a concurrent
	// CloseAdmission cannot be observed between the admission check and the
	// WaitGroup Add, preventing a syncWG.Add/Wait race when the counter is zero.
	admitMu sync.Mutex
	// admissionClosed is set when graceful shutdown begins; new lifecycle-managed
	// sync workers are then refused so none start after dependency teardown begins.
	admissionClosed bool
}

const masterDataSyncLogComponent = "master-data-sync"

var ErrSyncInProgress = errors.New("master data sync is already running")
var ErrRegionNotFound = errors.New("master data region not found")

// ErrShutdownAdmission is returned by lifecycle-managed sync starts (StartSync and
// the internal sync used by SyncAll/SyncRegion/RecoverInterruptedSync) when
// graceful shutdown has closed the admission gate, so no new sync worker is
// admitted after dependency teardown begins.
var ErrShutdownAdmission = errors.New("master data sync admission closed during shutdown")

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

func (usecase *MasterDataSyncUsecase) SetBackupStore(store MasterDataPayloadBackupStore) {
	if usecase == nil {
		return
	}

	usecase.backupStore = store
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

func (usecase *MasterDataSyncUsecase) SetJobTimeout(timeout time.Duration) {
	if usecase == nil {
		return
	}
	if timeout < 0 {
		timeout = 0
	}
	usecase.jobTimeout = timeout
}

// SetLeaseCoordinator enables cross-pod sync coordination. heartbeatInterval
// is how often a running job renews its lease; non-positive values are
// clamped to a 1s floor to avoid a busy renewal loop.
func (usecase *MasterDataSyncUsecase) SetLeaseCoordinator(coordinator MasterDataSyncLeaseCoordinator, heartbeatInterval time.Duration) {
	if usecase == nil {
		return
	}
	if coordinator == nil {
		return
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = time.Second
	}
	usecase.leaseCoordinator = coordinator
	usecase.leaseHeartbeatTimeout = heartbeatInterval
}

// leaseReleaseBudget is the context budget for releasing the lease after a
// job ends. It defaults to 5 seconds and is overridable in tests.
func (usecase *MasterDataSyncUsecase) leaseReleaseBudget() time.Duration {
	if usecase.leaseReleaseTimeout > 0 {
		return usecase.leaseReleaseTimeout
	}
	return 5 * time.Second
}

// SyncLeaseState exposes the sync lease for webhook admission probes and
// diagnostics. It reports an unheld state when no coordinator is configured.
func (usecase *MasterDataSyncUsecase) SyncLeaseState(ctx context.Context) (masterdata.SyncLeaseState, error) {
	if usecase == nil || usecase.leaseCoordinator == nil {
		return masterdata.SyncLeaseState{}, nil
	}
	return usecase.leaseCoordinator.State(ctx)
}

// SetLifecycleContext registers the application lifecycle context used to cancel
// long-running background sync workers (admin StartSync, webhook SyncRegion)
// during graceful shutdown. A nil context disables lifecycle cancellation and
// falls back to a detached background context.
func (usecase *MasterDataSyncUsecase) SetLifecycleContext(ctx context.Context) {
	if usecase == nil {
		return
	}
	if ctx != nil {
		usecase.lifecycleDone = ctx.Done()
	} else {
		usecase.lifecycleDone = nil
	}
}

// CloseAdmission closes the lifecycle admission gate so no new sync workers are
// admitted during graceful shutdown. It is safe to call multiple times and is
// invoked by Wait before blocking on syncWG.
func (usecase *MasterDataSyncUsecase) CloseAdmission() {
	if usecase == nil {
		return
	}
	usecase.admitMu.Lock()
	usecase.admissionClosed = true
	usecase.admitMu.Unlock()
}

// Wait blocks until all lifecycle-managed sync workers have stopped. It first
// closes the admission gate (so no new worker is admitted) and then waits on
// syncWG. This ordering prevents a syncWG.Add/Wait race: any worker admitted
// before CloseAdmission holds admitMu across its Add, so Wait cannot observe a
// zero counter and then race with that Add.
func (usecase *MasterDataSyncUsecase) Wait() {
	if usecase == nil {
		return
	}
	usecase.CloseAdmission()
	usecase.syncWG.Wait()
}

// tryAdmitSyncWorker increments syncWG if admission is still open, returning true
// when the caller may proceed to start a lifecycle-managed sync worker. The
// admission check and the WaitGroup Add run atomically under admitMu, so a
// concurrent CloseAdmission (and the subsequent Wait) cannot race with this Add.
// Callers must ensure a matching syncWG.Done() when true is returned.
func (usecase *MasterDataSyncUsecase) tryAdmitSyncWorker() bool {
	usecase.admitMu.Lock()
	defer usecase.admitMu.Unlock()
	if usecase.admissionClosed {
		return false
	}
	usecase.syncWG.Add(1)
	return true
}

func (usecase *MasterDataSyncUsecase) SyncAll(ctx context.Context) error {
	ctx, span := tracing.StartSpan(ctx, "master_data.sync_all", attribute.Int("region.count", len(usecase.sources)))
	err := usecase.sync(ctx, false, usecase.sources)
	tracing.EndSpan(span, err)
	return err
}

func (usecase *MasterDataSyncUsecase) StartSync(ctx context.Context, region string, force bool) error {
	if usecase == nil {
		return ErrRegionNotFound
	}

	targetRegion := strings.ToLower(strings.TrimSpace(region))
	targetSources := usecase.sources
	if targetRegion != "" {
		targetSources = nil
		for _, source := range usecase.sources {
			if strings.EqualFold(strings.TrimSpace(source.Region), targetRegion) {
				targetSources = []masterdata.Source{source}
				break
			}
		}
		if len(targetSources) == 0 {
			return ErrRegionNotFound
		}
	}

	if !usecase.syncRunning.CompareAndSwap(false, true) {
		usecase.logf("sync skipped reason=already_running")
		return ErrSyncInProgress
	}

	// Lifecycle admission gate: once shutdown has closed admission, do not admit
	// a new sync worker. tryAdmitSyncWorker atomically checks the gate and
	// increments syncWG, so the Add cannot race with the shutdown-time Wait.
	if !usecase.tryAdmitSyncWorker() {
		usecase.syncRunning.Store(false)
		usecase.logf("sync skipped reason=shutdown_admission_closed")
		return ErrShutdownAdmission
	}

	// Preserve the request trace but stay cancellable by the application
	// lifecycle, so a graceful shutdown can interrupt an in-flight admin sync
	// instead of leaving it orphaned.
	tracedContext := logging.DetachedTraceContext(ctx)
	go func() {
		// The worker context and its cancel are created and released inside the
		// worker goroutine (godre/S8188): defer cancelWorker immediately after
		// context.WithCancel so the derived timer/context is never leaked.
		workerContext, cancelWorker := context.WithCancel(tracedContext)
		defer cancelWorker()
		defer usecase.syncWG.Done()
		defer usecase.syncRunning.Store(false)

		// Cancel the worker when the app lifecycle ends (graceful shutdown). The
		// done channel is watched instead of a context.Context to avoid storing a
		// context on the struct; the goroutine exits once the derived context is
		// cancelled, so it never leaks.
		if lifecycleDone := usecase.lifecycleDone; lifecycleDone != nil {
			go func() {
				select {
				case <-lifecycleDone:
					cancelWorker()
				case <-workerContext.Done():
				}
			}()
		}

		err := usecase.syncLeased(workerContext, force, targetSources)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				usecase.logf("admin sync worker interrupted by shutdown region=%s force=%t", targetRegion, force)
				return
			}
			usecase.logf("admin sync worker failed region=%s force=%t error=%v", targetRegion, force, err)
			return
		}
		usecase.logf("admin sync worker completed region=%s force=%t", targetRegion, force)
	}()

	return nil
}

func (usecase *MasterDataSyncUsecase) SyncAllForce(ctx context.Context) error {
	ctx, span := tracing.StartSpan(ctx, "master_data.sync_all", attribute.Bool("force", true), attribute.Int("region.count", len(usecase.sources)))
	err := usecase.sync(ctx, true, usecase.sources)
	tracing.EndSpan(span, err)
	return err
}

// resolveRegionSources normalizes the requested region and returns the matching
// configured sources, or ErrRegionNotFound when the region is empty or unknown.
// It collapses the identical region-resolution boilerplate shared by SyncRegion
// and SyncRegionForce.
func (usecase *MasterDataSyncUsecase) resolveRegionSources(region string) ([]masterdata.Source, error) {
	targetRegion := strings.ToLower(strings.TrimSpace(region))
	if targetRegion == "" {
		return nil, ErrRegionNotFound
	}

	targetSources := make([]masterdata.Source, 0, 1)
	for _, source := range usecase.sources {
		if strings.EqualFold(strings.TrimSpace(source.Region), targetRegion) {
			targetSources = append(targetSources, source)
			break
		}
	}

	if len(targetSources) == 0 {
		return nil, ErrRegionNotFound
	}
	return targetSources, nil
}

func (usecase *MasterDataSyncUsecase) SyncRegion(ctx context.Context, region string) error {
	targetSources, err := usecase.resolveRegionSources(region)
	if err != nil {
		return err
	}
	ctx, span := tracing.StartSpan(ctx, "master_data.sync_region", attribute.String("region", strings.ToLower(strings.TrimSpace(region))))
	err = usecase.sync(ctx, false, targetSources)
	tracing.EndSpan(span, err)
	return err
}

func (usecase *MasterDataSyncUsecase) SyncRegionForce(ctx context.Context, region string) error {
	targetSources, err := usecase.resolveRegionSources(region)
	if err != nil {
		return err
	}
	ctx, span := tracing.StartSpan(ctx, "master_data.sync_region", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.Bool("force", true))
	err = usecase.sync(ctx, true, targetSources)
	tracing.EndSpan(span, err)
	return err
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
	ctx, span := tracing.StartSpan(ctx, "master_data.recover_interrupted_sync")
	var regions []string
	var err error
	defer func() {
		span.SetAttributes(attribute.Int("region.count", len(regions)))
		tracing.EndSpan(span, err)
	}()

	regions, err = usecase.InterruptedRegions(ctx)
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

	err = usecase.sync(ctx, false, targetSources)
	return regions, err
}

func (usecase *MasterDataSyncUsecase) sync(ctx context.Context, force bool, sources []masterdata.Source) error {
	// Lifecycle admission gate: once shutdown has closed admission, refuse new
	// sync workers so none start after dependency teardown begins. The check and
	// the WaitGroup Add run atomically under admitMu (see tryAdmitSyncWorker),
	// preventing a syncWG.Add/Wait race with the shutdown-time Wait.
	if !usecase.tryAdmitSyncWorker() {
		usecase.logf("sync skipped reason=shutdown_admission_closed")
		return ErrShutdownAdmission
	}
	defer usecase.syncWG.Done()
	if !usecase.syncRunning.CompareAndSwap(false, true) {
		usecase.logf("sync skipped reason=already_running")
		return ErrSyncInProgress
	}
	defer usecase.syncRunning.Store(false)
	return usecase.syncLeased(ctx, force, sources)
}

// syncLeased claims the cross-pod sync lease around syncClaimed. A job runs
// only while it holds the lease: a heartbeat goroutine renews ownership every
// leaseHeartbeatTimeout and cancels the job context as soon as renewal fails
// (lease lost or coordinator unreachable), which routes the job into the same
// recoverable-interruption path as graceful shutdown. Status writes carry the
// claim's fencing token so the fenced status store rejects a stale owner that
// keeps writing after a takeover.
func (usecase *MasterDataSyncUsecase) syncLeased(ctx context.Context, force bool, sources []masterdata.Source) error {
	if usecase.leaseCoordinator == nil {
		return usecase.syncClaimed(ctx, force, sources)
	}

	claim, err := usecase.leaseCoordinator.Acquire(ctx)
	if err != nil {
		if errors.Is(err, masterdata.ErrSyncLeaseHeld) {
			usecase.logf("sync skipped reason=lease_held_elsewhere")
			return err
		}
		usecase.logf("sync lease acquire failed error=%v", err)
		return fmt.Errorf("acquire sync lease: %w", err)
	}

	// The release context is created inside the deferred function so its
	// budget covers only the release call itself; a context created before the
	// sync would already be expired by the time a real (multi-second) sync
	// finished and the deferred release ran.
	defer func() {
		usecase.currentLeaseToken.Store(0)
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), usecase.leaseReleaseBudget())
		defer cancelRelease()
		if releaseErr := usecase.leaseCoordinator.Release(releaseCtx, claim); releaseErr != nil {
			usecase.logf("sync lease release failed holder=%s token=%d error=%v", claim.Holder, claim.Token, releaseErr)
		}
	}()

	usecase.currentLeaseToken.Store(claim.Token)
	usecase.logf("sync lease acquired holder=%s token=%d", claim.Holder, claim.Token)

	jobCtx, cancelJob := context.WithCancel(ctx)
	heartbeatDone := usecase.startLeaseHeartbeat(jobCtx, cancelJob, claim)

	err = usecase.syncClaimed(jobCtx, force, sources)

	cancelJob()
	<-heartbeatDone
	return err
}

// startLeaseHeartbeat renews the lease until the job context ends. The
// returned channel closes when the heartbeat goroutine has fully stopped, so
// the caller can order lease release after the last renewal attempt. A failed
// renewal cancels the job through cancelJob.
func (usecase *MasterDataSyncUsecase) startLeaseHeartbeat(jobCtx context.Context, cancelJob context.CancelFunc, claim masterdata.SyncLeaseClaim) <-chan struct{} {
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(usecase.leaseHeartbeatTimeout)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				renewCtx, cancelRenew := context.WithTimeout(context.WithoutCancel(jobCtx), 5*time.Second)
				renewErr := usecase.leaseCoordinator.Renew(renewCtx, claim)
				cancelRenew()
				if renewErr != nil {
					usecase.logf("sync lease lost holder=%s token=%d error=%v", claim.Holder, claim.Token, renewErr)
					cancelJob()
					return
				}
			}
		}
	}()
	return heartbeatDone
}

// isTerminalSyncStatus reports whether a sync status is a terminal one (success
// or failed) that should not be persisted when the run was interrupted by a
// graceful-shutdown cancellation. Recoverable statuses ("running"/"pending") are
// always safe to persist.
func isTerminalSyncStatus(status string) bool {
	return status == "success" || status == "failed"
}

// isInterrupted reports whether the region sync stopped because the application
// lifecycle context was cancelled (graceful shutdown) rather than due to a real
// error. In that case the per-region status is intentionally left in its prior
// recoverable ("running"/"pending") state instead of being marked failed, so
// interrupted-sync recovery can resume it.
func (usecase *MasterDataSyncUsecase) isInterrupted(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)
}

func (usecase *MasterDataSyncUsecase) syncClaimed(ctx context.Context, force bool, sources []masterdata.Source) error {
	if usecase.jobTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, usecase.jobTimeout)
		defer cancel()
	}

	ctx, span := tracing.StartSpan(ctx, "master_data.sync", attribute.Bool("force", force), attribute.Int("region.count", len(sources)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

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

			task := &regionSyncTask{
				usecase:       usecase,
				source:        source,
				force:         force,
				step:          step,
				totalSteps:    totalSteps,
				startedAt:     time.Now().UTC(),
				now:           time.Now().UTC(),
				recordFailure: recordFailure,
			}
			task.previous, task.hasPreviousStatus = previousStatuses[source.Region]
			task.syncRegion(ctx, regionCtx)
		})
	}

	wg.Wait()

	return usecase.finishSyncJob(ctx, regions, failedRegions, syncErrors, syncStartedAt)
}

// regionSyncTask carries the per-region state previously captured by the
// worker closure in syncClaimed. Each sync phase is a small method on this
// type so the overall flow stays readable without changing behavior; the job
// and region contexts are passed explicitly to each phase.
type regionSyncTask struct {
	usecase           *MasterDataSyncUsecase
	source            masterdata.Source
	previous          masterdata.SyncStatus
	hasPreviousStatus bool
	force             bool
	step              int
	totalSteps        int
	now               time.Time
	startedAt         time.Time
	resolvedCommit    string
	cacheReady        bool
	recordFailure     func(region string, err error)
}

// syncRegion runs one region through bootstrap restore, shortcut checks, and
// the full sync path. Failures are reported through recordFailure.
func (task *regionSyncTask) syncRegion(ctx, regionCtx context.Context) {
	if task.tryBootstrapRestoreFromLocalBackup(regionCtx) {
		return
	}
	if !task.ensureCacheReady(regionCtx) {
		return
	}
	if !task.cacheReady {
		task.persistPendingStatusWhenCacheCold(ctx)
	}
	if task.maybeSkipRegionSync(ctx, regionCtx) {
		return
	}
	task.runRegionFullSync(ctx, regionCtx)
}

// tryBootstrapRestoreFromLocalBackup restores a region without persisted sync
// status straight from the latest local backup. It reports whether the region
// was restored, in which case the full sync is skipped.
func (task *regionSyncTask) tryBootstrapRestoreFromLocalBackup(regionCtx context.Context) bool {
	if task.force || !task.usecase.restoreFromLocalBackupWithoutStatus || task.hasPreviousStatus {
		return false
	}

	restored, restoreErr := task.usecase.restoreRegionFromLatestLocalBackup(regionCtx, task.source, task.step, task.totalSteps)
	if restoreErr != nil {
		task.usecase.logf("sync local bootstrap restore failed region=%s error=%v", task.source.Region, restoreErr)
		return false
	}
	return restored
}

// ensureCacheReady checks the Redis cache for the region and records a
// failure when the readiness check itself errors.
func (task *regionSyncTask) ensureCacheReady(regionCtx context.Context) bool {
	cacheReady, cacheReadyErr := task.usecase.regionCacheReady(regionCtx, task.source.Region)
	if cacheReadyErr != nil {
		task.recordFailure(task.source.Region, fmt.Errorf("check cache readiness for region %s: %w", task.source.Region, cacheReadyErr))
		return false
	}
	task.cacheReady = cacheReady
	return true
}

// persistPendingStatusWhenCacheCold marks a cold region as pending so the
// dashboard can show it before the full sync finishes.
func (task *regionSyncTask) persistPendingStatusWhenCacheCold(ctx context.Context) {
	pendingCommit := ""
	if task.hasPreviousStatus {
		pendingCommit = strings.TrimSpace(task.previous.SourceCommit)
	}

	if err := task.usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         task.source.Region,
		Status:         "pending",
		FileCount:      0,
		SyncDurationMS: 0,
		LastSyncedAt:   task.now,
		SourceCommit:   pendingCommit,
		Source:         task.source,
		UpdatedAt:      task.now,
	}); err != nil {
		task.recordFailure(task.source.Region, fmt.Errorf("persist pending status for region %s: %w", task.source.Region, err))
	}
}

// maybeSkipRegionSync resolves the region's remote commit and attempts the
// unchanged-commit and changed-manifest shortcuts. It reports whether the
// region was fully handled without a full sync.
func (task *regionSyncTask) maybeSkipRegionSync(ctx, regionCtx context.Context) bool {
	resolver, ok := task.usecase.loader.(MasterDataSourceVersionResolver)
	if !ok {
		return false
	}

	commit, resolveErr := resolver.ResolveRegionVersion(regionCtx, task.source)
	if resolveErr != nil {
		task.usecase.logf("sync compare failed region=%s error=%v", task.source.Region, resolveErr)
		message := "compare commit failed, fallback to full sync"
		if task.force {
			message = "resolve commit failed, continue with force sync"
		}
		task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
			Event:       "master_data_sync_progress",
			Status:      "running",
			Region:      task.source.Region,
			Phase:       "compare",
			Message:     message,
			CurrentStep: task.step,
			TotalSteps:  task.totalSteps,
			UpdatedAt:   task.now,
		})
		return false
	}

	task.resolvedCommit = strings.TrimSpace(commit)
	task.usecase.logf("sync compare region=%s remote_commit=%s force=%t", task.source.Region, task.resolvedCommit, task.force)
	if task.force {
		return false
	}
	if task.trySkipUnchangedCommit(ctx, regionCtx) {
		return true
	}
	return task.trySkipChangedCommit(regionCtx)
}

// trySkipUnchangedCommit short-circuits a region whose remote commit matches
// the last successful sync, restoring state from Redis or the local backup.
func (task *regionSyncTask) trySkipUnchangedCommit(ctx, regionCtx context.Context) bool {
	if task.hasPreviousStatus && strings.EqualFold(strings.TrimSpace(task.previous.Status), "success") && task.previous.SourceCommit != "" && task.previous.SourceCommit == task.resolvedCommit {
		if task.trySkipViaRedisIndexRebuild(ctx, regionCtx) {
			return true
		}
		return task.trySkipViaLocalBackupRestore(ctx, regionCtx)
	}
	return false
}

// trySkipViaRedisIndexRebuild rebuilds the persisted search index from Redis
// and skips the sync when the version cache can be confirmed. It reports
// whether the region was skipped.
func (task *regionSyncTask) trySkipViaRedisIndexRebuild(ctx, regionCtx context.Context) bool {
	rebuilder, ok := task.usecase.cache.(MasterDataCacheIndexRebuilder)
	if !ok {
		return false
	}

	rebuilt, rebuildErr := rebuilder.RebuildRegionIndexFromRedis(regionCtx, task.source.Region)
	if rebuildErr != nil {
		task.usecase.logf("sync compare region=%s commit=%s redis_index_rebuild=failed error=%v", task.source.Region, task.resolvedCommit, rebuildErr)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but redis index rebuild failed, fallback to full sync", task.now)
		return false
	}
	if !rebuilt {
		task.usecase.logf("sync compare region=%s commit=%s redis_cache=empty fallback=full_sync", task.source.Region, task.resolvedCommit)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but redis cache missing, fallback to full sync", task.now)
		return false
	}
	if !task.usecase.ensureVersionCachePopulated(regionCtx, task.source, task.resolvedCommit, nil) {
		task.usecase.logf("sync compare region=%s commit=%s redis_index_rebuilt=true version_cache=missing fallback=full_sync", task.source.Region, task.resolvedCommit)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but version cache unavailable, fallback to full sync", task.now)
		return false
	}

	task.usecase.logf("sync skipped region=%s reason=commit_unchanged commit=%s index=rebuilt_from_redis", task.source.Region, task.resolvedCommit)
	task.publishRegionProgress(ctx, "success", "compare", "commit unchanged, rebuilt index from redis and skipped sync", task.now)
	return task.persistUnchangedSkipStatus(ctx)
}

// trySkipViaLocalBackupRestore restores the region cache from the local
// backup when the remote commit is unchanged. It reports whether the region
// was skipped.
func (task *regionSyncTask) trySkipViaLocalBackupRestore(ctx, regionCtx context.Context) bool {
	if task.usecase.backupStore == nil {
		return false
	}

	backupPayload, backupFound, backupErr := task.usecase.backupStore.LoadRegionPayload(regionCtx, task.source, task.resolvedCommit)
	if backupErr != nil {
		task.usecase.logf("sync compare region=%s commit=%s local_backup=load_failed error=%v", task.source.Region, task.resolvedCommit, backupErr)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but local backup read failed, fallback to full sync", task.now)
		return false
	}
	if !backupFound {
		task.usecase.logf("sync compare region=%s commit=%s local_backup=missing fallback=full_sync", task.source.Region, task.resolvedCommit)
		return false
	}
	return task.restoreCacheFromLocalBackup(ctx, regionCtx, backupPayload)
}

// restoreCacheFromLocalBackup stores the backup payload into the cache,
// confirms the version cache, and skips the sync on success.
func (task *regionSyncTask) restoreCacheFromLocalBackup(ctx, regionCtx context.Context, backupPayload map[string]any) bool {
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         task.source.Region,
		Phase:          "cache",
		Message:        "restoring cache from local backup",
		CurrentStep:    task.step,
		TotalSteps:     task.totalSteps,
		FileCount:      len(backupPayload),
		ProcessedFiles: 0,
		TotalFiles:     len(backupPayload),
		UpdatedAt:      time.Now().UTC(),
	})
	if cacheErr := task.usecase.cache.StoreRegion(regionCtx, task.source.Region, backupPayload); cacheErr != nil {
		task.usecase.logf("sync compare region=%s commit=%s local_backup=restore_failed error=%v", task.source.Region, task.resolvedCommit, cacheErr)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but local backup restore failed, fallback to full sync", task.now)
		return false
	}
	if !task.usecase.ensureVersionCachePopulated(regionCtx, task.source, task.resolvedCommit, backupPayload) {
		task.usecase.logf("sync compare region=%s commit=%s local_backup=version_cache_missing fallback=full_sync", task.source.Region, task.resolvedCommit)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but version cache unavailable from local backup, fallback to full sync", task.now)
		return false
	}

	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         task.source.Region,
		Phase:          "cache",
		Message:        "local backup cache restore completed",
		CurrentStep:    task.step,
		TotalSteps:     task.totalSteps,
		FileCount:      len(backupPayload),
		ProcessedFiles: len(backupPayload),
		TotalFiles:     len(backupPayload),
		UpdatedAt:      time.Now().UTC(),
	})
	task.usecase.logf("sync skipped region=%s reason=commit_unchanged commit=%s index=restored_from_local_backup", task.source.Region, task.resolvedCommit)
	task.publishRegionProgress(ctx, "success", "compare", "commit unchanged, restored cache from local backup and skipped sync", task.now)
	return task.persistUnchangedSkipStatus(ctx)
}

// trySkipChangedCommit reuses the local backup when the remote commit changed
// but the versions manifest did not. It reports whether the region was
// handled (skipped, or a persist error was recorded).
func (task *regionSyncTask) trySkipChangedCommit(regionCtx context.Context) bool {
	if task.hasPreviousStatus && strings.EqualFold(strings.TrimSpace(task.previous.Status), "success") && strings.TrimSpace(task.previous.SourceCommit) != "" && task.resolvedCommit != "" && strings.TrimSpace(task.previous.SourceCommit) != task.resolvedCommit {
		skipped, skipErr := task.usecase.trySkipRegionWithUnchangedManifest(
			regionCtx,
			task.source,
			task.previous,
			task.resolvedCommit,
			task.cacheReady,
			manifestSkipProgress{
				currentStep: task.step,
				totalSteps:  task.totalSteps,
				updatedAt:   task.now,
			},
		)
		if skipErr != nil {
			task.recordFailure(task.source.Region, fmt.Errorf("persist manifest skip status for region %s: %w", task.source.Region, skipErr))
			return true
		}
		return skipped
	}
	return false
}

// persistUnchangedSkipStatus re-persists the previous successful status with
// the resolved commit after a shortcut skip. Persist failures are recorded
// for the job result; the region is handled either way.
func (task *regionSyncTask) persistUnchangedSkipStatus(ctx context.Context) bool {
	skippedAt := time.Now().UTC()
	if statusErr := task.usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         task.previous.Region,
		Status:         task.previous.Status,
		FileCount:      task.previous.FileCount,
		SyncDurationMS: 0,
		LastSyncedAt:   task.previous.LastSyncedAt,
		SourceCommit:   task.resolvedCommit,
		ErrorMessage:   "",
		Source:         task.source,
		UpdatedAt:      skippedAt,
	}); statusErr != nil {
		task.recordFailure(task.source.Region, fmt.Errorf("persist unchanged status for region %s: %w", task.source.Region, statusErr))
	}
	return true
}

// publishRegionProgress emits the common per-region progress event shape.
func (task *regionSyncTask) publishRegionProgress(ctx context.Context, status, phase, message string, updatedAt time.Time) {
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      status,
		Region:      task.source.Region,
		Phase:       phase,
		Message:     message,
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		UpdatedAt:   updatedAt,
	})
}

// runRegionFullSync loads the region payload from the source, stores it in
// Redis, mirrors it to the local backup, and persists the success status.
func (task *regionSyncTask) runRegionFullSync(ctx, regionCtx context.Context) {
	task.persistRunningStatus(ctx)
	task.publishLoadPhase(ctx)

	collectorCtx := task.collectorContext(ctx, regionCtx)
	loadSource := task.source
	if task.resolvedCommit != "" {
		loadSource.Ref = task.resolvedCommit
	}

	payload, err := task.usecase.loader.LoadRegion(collectorCtx, loadSource)
	if err != nil {
		task.failRegionLoad(ctx, err)
		return
	}

	task.usecase.logf(
		"sync progress step=%d/%d region=%s phase=cache files=%d",
		task.step,
		task.totalSteps,
		task.source.Region,
		len(payload),
	)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         task.source.Region,
		Phase:          "cache",
		Message:        "writing cache",
		CurrentStep:    task.step,
		TotalSteps:     task.totalSteps,
		FileCount:      len(payload),
		ProcessedFiles: 0,
		TotalFiles:     len(payload),
		UpdatedAt:      time.Now().UTC(),
	})

	if task.storeRegionPayload(ctx, collectorCtx, payload) {
		return
	}
	if task.storeRegionVersionPayload(ctx, regionCtx, payload) {
		return
	}
	task.saveRegionBackup(regionCtx, payload)
	task.finishRegionSuccess(ctx, len(payload))
}

// persistRunningStatus marks the region as running before the load starts.
// Persist failures are logged but do not abort the sync.
func (task *regionSyncTask) persistRunningStatus(ctx context.Context) {
	if err := task.usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         task.source.Region,
		Status:         "running",
		FileCount:      0,
		SyncDurationMS: 0,
		LastSyncedAt:   task.now,
		SourceCommit:   task.resolvedCommit,
		Source:         task.source,
		UpdatedAt:      task.now,
	}); err != nil {
		task.usecase.logf("sync running status persist failed region=%s error=%v", task.source.Region, err)
	}
}

// publishLoadPhase logs and announces the load phase for the region.
func (task *regionSyncTask) publishLoadPhase(ctx context.Context) {
	task.usecase.logf(
		"sync progress step=%d/%d region=%s phase=load source=%s/%s ref=%s path=%s",
		task.step,
		task.totalSteps,
		task.source.Region,
		task.source.Owner,
		task.source.Repo,
		task.source.Ref,
		task.source.Path,
	)
	task.publishRegionProgress(ctx, "running", "load", "loading source files", task.now)
}

// collectorContext derives the digest-collecting load context, forwarding
// loader progress events with the region's step metadata.
func (task *regionSyncTask) collectorContext(ctx, regionCtx context.Context) context.Context {
	progressCtx := masterdata.WithProgressReporter(regionCtx, func(event masterdata.SyncUpdatedEvent) {
		if event.Event == "" {
			event.Event = "master_data_sync_progress"
		}
		if event.Status == "" {
			event.Status = "running"
		}
		if event.Region == "" {
			event.Region = task.source.Region
		}
		if event.CurrentStep == 0 {
			event.CurrentStep = task.step
		}
		if event.TotalSteps == 0 {
			event.TotalSteps = task.totalSteps
		}
		if event.UpdatedAt.IsZero() {
			event.UpdatedAt = time.Now().UTC()
		}

		task.usecase.publishSyncEvent(ctx, event)
	})
	collectorCtx := masterdata.NewSourceFileDigestCollector(progressCtx)
	if task.force {
		collectorCtx = masterdata.WithForceFullStore(collectorCtx)
	}
	return collectorCtx
}

// failRegionLoad handles a loader failure: shutdown interruptions return
// silently, rate limits fall back to the previous available state, and
// anything else is recorded as a region failure.
func (task *regionSyncTask) failRegionLoad(ctx context.Context, loadErr error) {
	if task.usecase.isInterrupted(ctx) {
		task.usecase.logf("sync interrupted by shutdown region=%s phase=load", task.source.Region)
		return
	}

	duration := time.Since(task.startedAt).Milliseconds()
	if isRateLimitError(loadErr) {
		fallbackErr := task.usecase.fallbackToPreviousAvailableState(ctx, task.source, task.previous, task.now)
		if fallbackErr == nil {
			task.usecase.logf("sync rate limit fallback applied region=%s duration_ms=%d", task.source.Region, duration)
			task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
				Event:       "master_data_sync_progress",
				Status:      "success",
				Region:      task.source.Region,
				Phase:       "fallback",
				Message:     "rate limit reached, fallback to previous available state",
				CurrentStep: task.step,
				TotalSteps:  task.totalSteps,
				DurationMS:  duration,
				UpdatedAt:   time.Now().UTC(),
			})
			return
		}
		task.usecase.logf("sync rate limit fallback failed region=%s error=%v", task.source.Region, fallbackErr)
	}

	task.usecase.logf("sync failed region=%s phase=load duration_ms=%d error=%v", task.source.Region, duration, loadErr)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "failed",
		Region:      task.source.Region,
		Phase:       "load",
		Message:     loadErr.Error(),
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		DurationMS:  duration,
		UpdatedAt:   time.Now().UTC(),
	})
	task.recordFailure(task.source.Region, loadErr)
	task.persistFailedStatus(ctx, 0, duration, loadErr.Error())
}

// storeRegionPayload writes the payload to Redis, preferring the digest-aware
// store path when the cache supports it. It reports whether the region failed.
func (task *regionSyncTask) storeRegionPayload(ctx, storeCtx context.Context, payload map[string]any) bool {
	fileDigests := masterdata.SourceFileDigestsFromContext(storeCtx).Snapshot()
	var storeErr error
	if digestStore, ok := task.usecase.cache.(MasterDataCacheSourceDigestStorer); ok && len(fileDigests) > 0 {
		storeErr = digestStore.StoreRegionWithSourceDigests(storeCtx, task.source.Region, payload, fileDigests)
	} else {
		storeErr = task.usecase.cache.StoreRegion(storeCtx, task.source.Region, payload)
	}
	if storeErr == nil {
		return false
	}

	duration := time.Since(task.startedAt).Milliseconds()
	task.usecase.logf("sync failed region=%s phase=cache files=%d duration_ms=%d error=%v", task.source.Region, len(payload), duration, storeErr)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "failed",
		Region:      task.source.Region,
		Phase:       "cache",
		Message:     storeErr.Error(),
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		FileCount:   len(payload),
		DurationMS:  duration,
		UpdatedAt:   time.Now().UTC(),
	})
	task.recordFailure(task.source.Region, storeErr)
	task.persistFailedStatus(ctx, len(payload), duration, storeErr.Error())
	return true
}

// storeRegionVersionPayload mirrors the versions payload found in the loaded
// files into the Redis version cache. It reports whether the region failed.
func (task *regionSyncTask) storeRegionVersionPayload(ctx, regionCtx context.Context, payload map[string]any) bool {
	versionStore, ok := task.usecase.cache.(MasterDataCacheVersionStorer)
	if !ok {
		return false
	}

	versionPayload, versionFound := versionPayloadFromBackup(task.source, payload)
	if !versionFound {
		return false
	}

	versionCacheErr := versionStore.StoreRegionVersionPayload(regionCtx, task.source.Region, versionPayload)
	if versionCacheErr == nil {
		return false
	}

	duration := time.Since(task.startedAt).Milliseconds()
	task.usecase.logf("sync failed region=%s phase=version-cache duration_ms=%d error=%v", task.source.Region, duration, versionCacheErr)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "failed",
		Region:      task.source.Region,
		Phase:       "version-cache",
		Message:     versionCacheErr.Error(),
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		FileCount:   len(payload),
		DurationMS:  duration,
		UpdatedAt:   time.Now().UTC(),
	})
	task.recordFailure(task.source.Region, versionCacheErr)
	task.persistFailedStatus(ctx, len(payload), duration, versionCacheErr.Error())
	return true
}

// saveRegionBackup mirrors the synced payload to the local backup; backup
// failures are logged but do not fail the region.
func (task *regionSyncTask) saveRegionBackup(regionCtx context.Context, payload map[string]any) {
	if task.usecase.backupStore == nil {
		return
	}
	if backupErr := task.usecase.backupStore.SaveRegionPayload(regionCtx, task.source, task.resolvedCommit, payload); backupErr != nil {
		task.usecase.logf("sync backup save failed region=%s commit=%s error=%v", task.source.Region, task.resolvedCommit, backupErr)
	}
}

// finishRegionSuccess publishes the success event and persists the region's
// success status; persist failures are recorded for the job result.
func (task *regionSyncTask) finishRegionSuccess(ctx context.Context, fileCount int) {
	duration := time.Since(task.startedAt).Milliseconds()
	task.usecase.logf("sync success region=%s files=%d duration_ms=%d", task.source.Region, fileCount, duration)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "success",
		Region:      task.source.Region,
		Phase:       "done",
		Message:     "region sync completed",
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		FileCount:   fileCount,
		DurationMS:  duration,
		UpdatedAt:   time.Now().UTC(),
	})

	completedAt := time.Now().UTC()
	if statusErr := task.usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         task.source.Region,
		Status:         "success",
		FileCount:      fileCount,
		SyncDurationMS: duration,
		LastSyncedAt:   completedAt,
		SourceCommit:   task.resolvedCommit,
		Source:         task.source,
		UpdatedAt:      completedAt,
	}); statusErr != nil {
		task.recordFailure(task.source.Region, fmt.Errorf("persist success status for region %s: %w", task.source.Region, statusErr))
	}
}

// persistFailedStatus records the region's failed status; persist failures
// are additionally recorded for the job result.
func (task *regionSyncTask) persistFailedStatus(ctx context.Context, fileCount int, durationMS int64, message string) {
	if statusErr := task.usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         task.source.Region,
		Status:         "failed",
		FileCount:      fileCount,
		SyncDurationMS: durationMS,
		LastSyncedAt:   task.now,
		SourceCommit:   task.resolvedCommit,
		ErrorMessage:   message,
		Source:         task.source,
		UpdatedAt:      task.now,
	}); statusErr != nil {
		task.recordFailure(task.source.Region, fmt.Errorf("persist failed status for region %s: %w", task.source.Region, statusErr))
	}
}

// finishSyncJob publishes the job-level result event. A shutdown-cancelled
// context reports interruption instead of success/failure so callers (and
// interrupted-sync recovery) do not treat the cancelled run as done.
func (usecase *MasterDataSyncUsecase) finishSyncJob(ctx context.Context, regions []string, failedRegions []string, syncErrors []error, syncStartedAt time.Time) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
			Event:     "master_data_updated",
			Status:    "interrupted",
			Regions:   regions,
			UpdatedAt: time.Now().UTC(),
		})
		usecase.logf(
			"sync interrupted by shutdown regions=%d duration_ms=%d",
			len(regions),
			time.Since(syncStartedAt).Milliseconds(),
		)
		return context.Canceled
	}

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

type manifestSkipProgress struct {
	currentStep int
	totalSteps  int
	updatedAt   time.Time
}

func (usecase *MasterDataSyncUsecase) trySkipRegionWithUnchangedManifest(ctx context.Context, source masterdata.Source, previous masterdata.SyncStatus, resolvedCommit string, cacheReady bool, progress manifestSkipProgress) (bool, error) {
	if !usecase.versionManifestsMatchForSkip(ctx, source, resolvedCommit) {
		return false, nil
	}

	latestPayload, backupCommit, _, payloadFound, err := usecase.backupStore.LoadLatestRegionPayload(ctx, source)
	if err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=latest_payload_load_error error=%v", source.Region, resolvedCommit, err)
		return false, nil
	}
	if !payloadFound {
		return false, nil
	}

	if strings.TrimSpace(backupCommit) != strings.TrimSpace(previous.SourceCommit) {
		usecase.logf("sync compare region=%s commit=%s local_backup=commit_mismatch backup_commit=%s fallback=full_sync", source.Region, resolvedCommit, strings.TrimSpace(backupCommit))
		return false, nil
	}

	if !cacheReady && !usecase.restoreManifestSkipCache(ctx, source, resolvedCommit, latestPayload, progress) {
		return false, nil
	}

	if versionStore, ok := usecase.cache.(MasterDataCacheVersionStorer); ok {
		if versionPayload, versionFound := versionPayloadFromBackup(source, latestPayload); versionFound {
			if versionCacheErr := versionStore.StoreRegionVersionPayload(ctx, source.Region, versionPayload); versionCacheErr != nil {
				usecase.logf("sync compare region=%s commit=%s reason=version_cache_store_error error=%v", source.Region, resolvedCommit, versionCacheErr)
				return false, nil
			}
		} else {
			usecase.logf("sync compare region=%s commit=%s reason=version_cache_unavailable", source.Region, resolvedCommit)
			return false, nil
		}
	}

	if err := usecase.backupStore.SaveRegionPayload(ctx, source, resolvedCommit, latestPayload); err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=backup_rebase_error error=%v", source.Region, resolvedCommit, err)
		return false, nil
	}

	skippedAt := time.Now().UTC()
	if err := usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         previous.Region,
		Status:         "success",
		FileCount:      len(latestPayload),
		SyncDurationMS: 0,
		LastSyncedAt:   previous.LastSyncedAt,
		SourceCommit:   resolvedCommit,
		ErrorMessage:   "",
		Source:         source,
		UpdatedAt:      skippedAt,
	}); err != nil {
		return true, err
	}

	usecase.logf("sync skipped region=%s reason=versions_manifest_unchanged previous_commit=%s commit=%s", source.Region, strings.TrimSpace(previous.SourceCommit), resolvedCommit)
	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "success",
		Region:      source.Region,
		Phase:       "compare",
		Message:     "versions manifest unchanged, reused local backup and skipped sync",
		CurrentStep: progress.currentStep,
		TotalSteps:  progress.totalSteps,
		FileCount:   len(latestPayload),
		UpdatedAt:   progress.updatedAt,
	})

	return true, nil
}

func (usecase *MasterDataSyncUsecase) versionManifestsMatchForSkip(ctx context.Context, source masterdata.Source, resolvedCommit string) bool {
	manifestLoader, ok := usecase.loader.(MasterDataSourceVersionManifestLoader)
	if !ok || usecase.backupStore == nil {
		return false
	}

	versionStore, ok := usecase.backupStore.(MasterDataVersionBackupStore)
	if !ok {
		return false
	}

	localManifest, _, _, localFound, err := versionStore.LoadLatestRegionVersionPayload(ctx, source)
	if err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=local_manifest_load_error error=%v", source.Region, resolvedCommit, err)
		return false
	}
	if !localFound {
		return false
	}

	manifestSource := source
	manifestSource.Ref = resolvedCommit
	remoteManifest, remoteFound, err := manifestLoader.LoadVersionManifest(ctx, manifestSource)
	if err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=remote_manifest_load_error error=%v", source.Region, resolvedCommit, err)
		return false
	}
	if !remoteFound {
		return false
	}

	matched, err := jsonValuesEqual(localManifest, remoteManifest)
	if err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=json_comparison_error error=%v", source.Region, resolvedCommit, err)
		return false
	}

	return matched
}

func (usecase *MasterDataSyncUsecase) restoreManifestSkipCache(ctx context.Context, source masterdata.Source, resolvedCommit string, latestPayload map[string]any, progress manifestSkipProgress) bool {
	if usecase.cache == nil {
		return false
	}

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         source.Region,
		Phase:          "cache",
		Message:        "restoring cache from local backup",
		CurrentStep:    progress.currentStep,
		TotalSteps:     progress.totalSteps,
		FileCount:      len(latestPayload),
		ProcessedFiles: 0,
		TotalFiles:     len(latestPayload),
		UpdatedAt:      time.Now().UTC(),
	})
	if err := usecase.cache.StoreRegion(ctx, source.Region, latestPayload); err != nil {
		usecase.logf("sync compare region=%s commit=%s reason=cache_store_region_error error=%v", source.Region, resolvedCommit, err)
		return false
	}

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         source.Region,
		Phase:          "cache",
		Message:        "local backup cache restore completed",
		CurrentStep:    progress.currentStep,
		TotalSteps:     progress.totalSteps,
		FileCount:      len(latestPayload),
		ProcessedFiles: len(latestPayload),
		TotalFiles:     len(latestPayload),
		UpdatedAt:      time.Now().UTC(),
	})

	return true
}

// ensureVersionCachePopulated checks whether the version payload is available in
// the version cache key. If the cache does not support separate version storage
// it returns true immediately (nothing to worry about). Otherwise it tries to
// load from cache, restore from the in-hand payload hint, or restore from a
// commit-matched local backup before giving up.
//
// The backup restore is strict: only a snapshot whose commit matches
// expectedCommit may be used, so a stale backup captured for a different commit
// never leaks its versions.json into the cache during a commit-unchanged
// shortcut. A missing version payload forces the caller to fall back to a full
// sync.
func (usecase *MasterDataSyncUsecase) ensureVersionCachePopulated(ctx context.Context, source masterdata.Source, expectedCommit string, payloadHint map[string]any) bool {
	versionStore, ok := usecase.cache.(MasterDataCacheVersionStorer)
	if !ok {
		return true
	}

	if usecase.versionCacheAlreadyLoaded(ctx, source) {
		return true
	}
	if usecase.tryRestoreVersionFromHint(ctx, source, versionStore, payloadHint) {
		return true
	}
	if usecase.tryRestoreVersionFromBackup(ctx, source, versionStore, expectedCommit) {
		return true
	}

	return false
}

func (usecase *MasterDataSyncUsecase) versionCacheAlreadyLoaded(ctx context.Context, source masterdata.Source) bool {
	versionLoader, ok := usecase.cache.(MasterDataCacheVersionLoader)
	if !ok {
		return false
	}
	_, found, loadErr := versionLoader.LoadRegionVersionPayload(ctx, source.Region)
	return loadErr == nil && found
}

func (usecase *MasterDataSyncUsecase) tryRestoreVersionFromHint(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, payloadHint map[string]any) bool {
	if payloadHint == nil {
		return false
	}
	return usecase.storeVersionFromPayload(ctx, source, versionStore, payloadHint)
}

func (usecase *MasterDataSyncUsecase) tryRestoreVersionFromBackup(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, expectedCommit string) bool {
	if usecase.backupStore == nil || expectedCommit == "" {
		return false
	}

	if payload, found, err := usecase.backupStore.LoadRegionPayload(ctx, source, expectedCommit); err == nil && found {
		if usecase.storeVersionFromPayload(ctx, source, versionStore, payload) {
			return true
		}
	}

	if usecase.restoreVersionFromLatestBackup(ctx, source, versionStore, expectedCommit) {
		return true
	}

	return false
}

// restoreVersionFromLatestBackup restores the version payload from the latest
// local backups, but only when the snapshot's commit matches expectedCommit.
func (usecase *MasterDataSyncUsecase) restoreVersionFromLatestBackup(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, expectedCommit string) bool {
	if usecase.restoreVersionFromVersionBackup(ctx, source, versionStore, expectedCommit) {
		return true
	}

	return usecase.restoreVersionFromPayloadBackup(ctx, source, versionStore, expectedCommit)
}

func (usecase *MasterDataSyncUsecase) restoreVersionFromVersionBackup(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, expectedCommit string) bool {
	versionBackupStore, ok := usecase.backupStore.(MasterDataVersionBackupStore)
	if !ok {
		return false
	}

	version, commit, _, found, err := versionBackupStore.LoadLatestRegionVersionPayload(ctx, source)
	if err != nil || !found || !strings.EqualFold(commit, expectedCommit) {
		return false
	}

	return usecase.restoreVersionPayload(ctx, source, versionStore, version)
}

func (usecase *MasterDataSyncUsecase) restoreVersionFromPayloadBackup(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, expectedCommit string) bool {
	payload, commit, _, found, err := usecase.backupStore.LoadLatestRegionPayload(ctx, source)
	if err != nil || !found || !strings.EqualFold(commit, expectedCommit) {
		return false
	}

	return usecase.storeVersionFromPayload(ctx, source, versionStore, payload)
}

func (usecase *MasterDataSyncUsecase) storeVersionFromPayload(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, payload map[string]any) bool {
	version, ok := versionPayloadFromBackup(source, payload)
	if !ok {
		return false
	}
	return usecase.restoreVersionPayload(ctx, source, versionStore, version)
}

func (usecase *MasterDataSyncUsecase) restoreVersionPayload(ctx context.Context, source masterdata.Source, versionStore MasterDataCacheVersionStorer, version any) bool {
	return versionStore.StoreRegionVersionPayload(ctx, source.Region, version) == nil
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

func (usecase *MasterDataSyncUsecase) RuntimeSearchIndexReadyRegions(ctx context.Context) ([]string, error) {
	regions, err := usecase.SuccessfulSyncRegions(ctx)
	if err != nil {
		return nil, err
	}

	readyRegions := make([]string, 0, len(regions))
	for _, region := range regions {
		cacheReady := usecase.regionCacheReadySnapshot(region)
		if !cacheReady {
			continue
		}

		readyRegions = append(readyRegions, region)
	}

	return readyRegions, nil
}

func (usecase *MasterDataSyncUsecase) SuccessfulSyncRegions(ctx context.Context) ([]string, error) {
	if usecase == nil || usecase.statusStore == nil {
		return nil, nil
	}

	statuses, err := usecase.statusStore.List(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(statuses))
	regions := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if !strings.EqualFold(strings.TrimSpace(status.Status), "success") {
			continue
		}

		region := strings.ToLower(strings.TrimSpace(status.Region))
		if region == "" {
			continue
		}
		if _, exists := seen[region]; exists {
			continue
		}

		seen[region] = struct{}{}
		regions = append(regions, region)
	}

	sort.Strings(regions)
	return regions, nil
}

func (usecase *MasterDataSyncUsecase) HasEntityRecords(ctx context.Context, region string, entity string) (bool, error) {
	if usecase == nil || usecase.cache == nil {
		return false, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	entity = strings.ToLower(strings.TrimSpace(entity))
	if region == "" || entity == "" {
		return false, nil
	}

	if inspector, ok := usecase.cache.(MasterDataCacheEntityInspector); ok {
		return inspector.HasEntityRecords(ctx, region, entity)
	}

	return false, nil
}

func (usecase *MasterDataSyncUsecase) HasSuccessfulSync(ctx context.Context, region string) (bool, error) {
	if usecase == nil || usecase.statusStore == nil {
		return false, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false, nil
	}

	statuses, err := usecase.statusStore.List(ctx)
	if err != nil {
		return false, fmt.Errorf("list master data sync statuses: %w", err)
	}

	for _, status := range statuses {
		if strings.EqualFold(strings.TrimSpace(status.Region), region) &&
			strings.EqualFold(strings.TrimSpace(status.Status), "success") {
			return true, nil
		}
	}

	return false, nil
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

func (usecase *MasterDataSyncUsecase) regionCacheReadySnapshot(region string) bool {
	if usecase == nil || usecase.cache == nil {
		return true
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false
	}

	if inspector, ok := usecase.cache.(MasterDataCacheIndexInspector); ok {
		return inspector.HasRegionIndex(region)
	}

	return true
}

func (usecase *MasterDataSyncUsecase) regionCacheReady(ctx context.Context, region string) (bool, error) {
	if usecase == nil || usecase.cache == nil {
		return true, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false, nil
	}

	if loader, ok := usecase.cache.(MasterDataCacheIndexLoader); ok {
		loaded, err := loader.LoadRegionIndexFromRedis(ctx, region)
		if err != nil {
			return false, fmt.Errorf("load region index %s: %w", region, err)
		}
		if loaded {
			return true, nil
		}
	}

	if rebuilder, ok := usecase.cache.(MasterDataCacheIndexRebuilder); ok {
		rebuilt, err := rebuilder.RebuildRegionIndexFromRedis(ctx, region)
		if err != nil {
			return false, fmt.Errorf("rebuild region index %s: %w", region, err)
		}
		if rebuilt {
			return true, nil
		}
	}

	_, canLoad := usecase.cache.(MasterDataCacheIndexLoader)
	_, canRebuild := usecase.cache.(MasterDataCacheIndexRebuilder)
	if !canLoad && !canRebuild {
		if inspector, ok := usecase.cache.(MasterDataCacheIndexInspector); ok {
			return inspector.HasRegionIndex(region), nil
		}
	}
	if !canLoad && !canRebuild {
		return true, nil
	}

	return false, nil
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

// RedisReady reports whether the Redis cache backend is reachable. It returns
// (true, nil) when Redis is reachable or when the configured cache does not
// support connectivity checks. It returns (false, err) when Redis is
// unreachable so the serve readiness probe can report the pod not ready.
func (usecase *MasterDataSyncUsecase) RedisReady(ctx context.Context) (bool, error) {
	if usecase == nil {
		return false, fmt.Errorf("master data sync usecase is nil")
	}
	if usecase.cache == nil {
		return false, fmt.Errorf("master data cache is nil")
	}

	pinger, ok := usecase.cache.(MasterDataRedisPinger)
	if !ok {
		return true, nil
	}

	if err := pinger.Ping(ctx); err != nil {
		return false, fmt.Errorf("redis readiness check: %w", err)
	}

	return true, nil
}

// RegionVersionReady reports whether usable version metadata for a region is
// available in the cache. The serve readiness probe requires persisted card
// records AND version metadata before considering a region ready, and the
// version payload must satisfy the same contract the public /versions response
// enforces (at least one valid version field). When the cache does not support
// version storage the check is treated as satisfied (true) so a
// non-version-aware cache does not block readiness.
func (usecase *MasterDataSyncUsecase) RegionVersionReady(ctx context.Context, region string) (bool, error) {
	if usecase == nil {
		return false, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false, nil
	}

	loader, ok := usecase.cache.(MasterDataCacheVersionLoader)
	if !ok {
		return true, nil
	}

	payload, found, err := loader.LoadRegionVersionPayload(ctx, region)
	if err != nil {
		// A corrupt/malformed version payload is a data problem (surfaced by the
		// caller as master_data), not a Redis connectivity failure. Only genuine
		// Redis transport/read failures propagate as errors so the readiness probe
		// reports the redis dependency instead of master_data.
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			return false, nil
		}
		return false, fmt.Errorf("load region version payload %s: %w", region, err)
	}
	if !found {
		return false, nil
	}

	payloadMap, ok := payload.(map[string]any)
	if !ok {
		return false, nil
	}

	return masterdata.IsCompleteVersionPayload(payloadMap), nil
}

func (usecase *MasterDataSyncUsecase) VersionByRegion(ctx context.Context, region string) (any, bool, error) {
	if usecase == nil {
		return nil, false, nil
	}

	source, found := usecase.sourceByRegion(region)
	if !found {
		return nil, false, nil
	}

	if loader, ok := usecase.cache.(MasterDataCacheVersionLoader); ok {
		version, loadFound, loadErr := loader.LoadRegionVersionPayload(ctx, source.Region)
		if loadErr != nil {
			usecase.logf("version_by_region region=%s reason=versions_cache_load_error error=%v", source.Region, loadErr)
		} else if version != nil && loadFound {
			return version, true, nil
		}
	}

	if usecase.backupStore == nil {
		return nil, false, nil
	}

	if versionStore, ok := usecase.backupStore.(MasterDataVersionBackupStore); ok {
		version, _, _, versionFound, err := versionStore.LoadLatestRegionVersionPayload(ctx, source)
		if err != nil || versionFound {
			return version, versionFound, err
		}
	}

	payload, _, _, found, err := usecase.backupStore.LoadLatestRegionPayload(ctx, source)
	if err != nil || !found {
		return nil, found, err
	}

	version, found := versionPayloadFromBackup(source, payload)
	if !found {
		return nil, false, nil
	}

	return version, true, nil
}

func (usecase *MasterDataSyncUsecase) WarmConfiguredRegionIndexes(ctx context.Context) ([]string, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.warm_region_indexes")
	var warmed []string
	var err error
	defer func() {
		span.SetAttributes(attribute.Int("region.count", len(warmed)))
		tracing.EndSpan(span, err)
	}()

	loader, ok := usecase.cache.(MasterDataCacheIndexLoader)
	if !ok {
		return nil, nil
	}

	regions := usecase.ConfiguredRegions()
	warmed = make([]string, 0, len(regions))
	for _, region := range regions {
		loaded, loadErr := loader.LoadRegionIndexFromRedis(ctx, region)
		if loadErr != nil {
			err = fmt.Errorf("warm region index %s: %w", region, loadErr)
			return warmed, err
		}
		if loaded {
			warmed = append(warmed, region)
		}
	}

	return warmed, nil
}

func (usecase *MasterDataSyncUsecase) sourceByRegion(region string) (masterdata.Source, bool) {
	targetRegion := strings.ToLower(strings.TrimSpace(region))
	if targetRegion == "" {
		return masterdata.Source{}, false
	}

	for _, source := range usecase.sources {
		if strings.EqualFold(strings.TrimSpace(source.Region), targetRegion) {
			return source, true
		}
	}

	return masterdata.Source{}, false
}

func versionPayloadFromBackup(source masterdata.Source, payload map[string]any) (any, bool) {
	if len(payload) == 0 {
		return nil, false
	}

	candidates := []string{"versions.json"}
	if trimmedPath := strings.Trim(strings.TrimSpace(source.Path), "/"); trimmedPath != "" {
		candidates = append([]string{path.Join(trimmedPath, "versions.json")}, candidates...)
	}

	for _, candidate := range candidates {
		if value, ok := payload[candidate]; ok {
			return value, true
		}
	}

	for filePath, value := range payload {
		if strings.EqualFold(path.Base(strings.TrimSpace(filePath)), "versions.json") {
			return value, true
		}
	}

	return nil, false
}

func (usecase *MasterDataSyncUsecase) EnsureConfiguredRegionIndexes(ctx context.Context) ([]string, []string, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.ensure_region_indexes")
	var loadedRegions []string
	var rebuiltRegions []string
	var err error
	defer func() {
		span.SetAttributes(
			attribute.Int("region.loaded.count", len(loadedRegions)),
			attribute.Int("region.rebuilt.count", len(rebuiltRegions)),
		)
		tracing.EndSpan(span, err)
	}()

	regions := usecase.ConfiguredRegions()
	if len(regions) == 0 {
		return nil, nil, nil
	}

	loader, canLoad := usecase.cache.(MasterDataCacheIndexLoader)
	rebuilder, canRebuild := usecase.cache.(MasterDataCacheIndexRebuilder)
	if !canLoad && !canRebuild {
		return nil, nil, nil
	}

	loadedRegions = make([]string, 0, len(regions))
	rebuiltRegions = make([]string, 0, len(regions))
	for _, region := range regions {
		if canLoad {
			loaded, err := loader.LoadRegionIndexFromRedis(ctx, region)
			if err != nil {
				err = fmt.Errorf("ensure region index %s load: %w", region, err)
				return loadedRegions, rebuiltRegions, err
			}
			if loaded {
				loadedRegions = append(loadedRegions, region)
				continue
			}
		}

		if canRebuild {
			rebuilt, err := rebuilder.RebuildRegionIndexFromRedis(ctx, region)
			if err != nil {
				err = fmt.Errorf("ensure region index %s rebuild: %w", region, err)
				return loadedRegions, rebuiltRegions, err
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
	ctx, span := tracing.StartSpan(ctx, "master_data.get_by_id", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return nil, false, nil
	}

	record, found, err := usecase.cache.GetByID(ctx, region, entity, id)
	span.SetAttributes(attribute.Bool("cache.hit", found))
	return record, found, err
}

func (usecase *MasterDataSyncUsecase) ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.list_by_page", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))), attribute.Int("page.size", pageSize))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return []map[string]any{}, 0, nil
	}

	items, total, err := usecase.cache.ListByPage(ctx, region, entity, page, pageSize)
	span.SetAttributes(attribute.Int("result.count", len(items)), attribute.Int("result.total", total))
	return items, total, err
}

func (usecase *MasterDataSyncUsecase) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.list_all", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return []map[string]any{}, nil
	}

	items, err := usecase.cache.ListAll(ctx, region, entity)
	span.SetAttributes(attribute.Int("result.count", len(items)))
	return items, err
}

func (usecase *MasterDataSyncUsecase) Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.search", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))), attribute.Int("search.field.count", len(fields)), attribute.Int("search.limit", limit))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return []masterdata.SearchMatch{}, nil
	}

	matches, err := usecase.cache.Search(ctx, region, entity, query, fields, limit)
	span.SetAttributes(attribute.Int("result.count", len(matches)))
	return matches, err
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
	usecase.logf("current_event check_start region=%s now_ms=%d now=%s", normalizedRegion, nowMillis, now.UTC().Format(time.RFC3339))

	cached, found, err := usecase.readCurrentEventCache(ctx, normalizedRegion)
	if err != nil {
		return nil, false, err
	}
	if !found {
		usecase.logf("current_event cache_check region=%s stage=initial found=false", normalizedRegion)
	} else {
		evaluation := evaluateCurrentEventRange(cached, nowMillis)
		usecase.logf(
			"current_event cache_check region=%s stage=initial found=true id=%s name=%q start_ms=%d start=%s end_ms=%d end=%s in_range=%t reason=%s raw_times=%s",
			normalizedRegion,
			currentEventRecordID(cached),
			currentEventRecordName(cached),
			evaluation.StartAt,
			formatCurrentEventTimestamp(evaluation.StartAt),
			evaluation.EndAt,
			formatCurrentEventTimestamp(evaluation.EndAt),
			evaluation.InRange,
			evaluation.Reason,
			currentEventRawTimes(cached),
		)
	}
	if found && isEventInTimeRange(cached, nowMillis) {
		usecase.logf("current_event cache_hit region=%s stage=initial id=%s", normalizedRegion, currentEventRecordID(cached))
		return cached, true, nil
	}

	regionLock := usecase.currentEventLock(normalizedRegion)
	regionLock.Lock()
	defer regionLock.Unlock()

	cached, found, err = usecase.readCurrentEventCache(ctx, normalizedRegion)
	if err != nil {
		return nil, false, err
	}
	if !found {
		usecase.logf("current_event cache_check region=%s stage=locked found=false", normalizedRegion)
	} else {
		evaluation := evaluateCurrentEventRange(cached, nowMillis)
		usecase.logf(
			"current_event cache_check region=%s stage=locked found=true id=%s name=%q start_ms=%d start=%s end_ms=%d end=%s in_range=%t reason=%s raw_times=%s",
			normalizedRegion,
			currentEventRecordID(cached),
			currentEventRecordName(cached),
			evaluation.StartAt,
			formatCurrentEventTimestamp(evaluation.StartAt),
			evaluation.EndAt,
			formatCurrentEventTimestamp(evaluation.EndAt),
			evaluation.InRange,
			evaluation.Reason,
			currentEventRawTimes(cached),
		)
	}
	if found && isEventInTimeRange(cached, nowMillis) {
		usecase.logf("current_event cache_hit region=%s stage=locked id=%s", normalizedRegion, currentEventRecordID(cached))
		return cached, true, nil
	}

	current, found, err := usecase.findCurrentEvent(ctx, normalizedRegion, nowMillis)
	if err != nil {
		return nil, false, err
	}
	if found {
		evaluation := evaluateCurrentEventRange(current, nowMillis)
		usecase.logf(
			"current_event refresh_result region=%s found=true id=%s name=%q start_ms=%d start=%s end_ms=%d end=%s in_range=%t reason=%s raw_times=%s",
			normalizedRegion,
			currentEventRecordID(current),
			currentEventRecordName(current),
			evaluation.StartAt,
			formatCurrentEventTimestamp(evaluation.StartAt),
			evaluation.EndAt,
			formatCurrentEventTimestamp(evaluation.EndAt),
			evaluation.InRange,
			evaluation.Reason,
			currentEventRawTimes(current),
		)
	} else {
		usecase.logf("current_event refresh_result region=%s found=false", normalizedRegion)
	}

	payload := map[string]any{"currentEvents.json": []any{}}
	if found {
		payload["currentEvents.json"] = []any{current}
	}

	if err := usecase.cache.StoreRegion(ctx, normalizedRegion, payload); err != nil {
		return nil, false, fmt.Errorf("store current event cache region %s: %w", normalizedRegion, err)
	}
	usecase.logf("current_event cache_write region=%s found=%t id=%s", normalizedRegion, found, currentEventRecordID(current))

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

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "running",
		Region:      source.Region,
		Phase:       "bootstrap",
		Message:     "comparing local versions.json with remote",
		CurrentStep: currentStep,
		TotalSteps:  totalSteps,
		UpdatedAt:   time.Now().UTC(),
	})

	canRestore, reason, compareErr := usecase.canRestoreFromLocalBackupByVersions(ctx, source, backupPayload)
	if compareErr != nil {
		usecase.logf("sync bootstrap local backup compare failed region=%s reason=%s error=%v", source.Region, reason, compareErr)
		usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
			Event:       "master_data_sync_progress",
			Status:      "running",
			Region:      source.Region,
			Phase:       "bootstrap",
			Message:     "local versions.json compare failed, fallback to remote sync",
			CurrentStep: currentStep,
			TotalSteps:  totalSteps,
			UpdatedAt:   time.Now().UTC(),
		})
		return false, nil
	}
	if !canRestore {
		usecase.logf("sync bootstrap local backup skipped region=%s reason=%s", source.Region, reason)
		usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
			Event:       "master_data_sync_progress",
			Status:      "running",
			Region:      source.Region,
			Phase:       "bootstrap",
			Message:     "local versions.json mismatch remote, fallback to remote sync",
			CurrentStep: currentStep,
			TotalSteps:  totalSteps,
			UpdatedAt:   time.Now().UTC(),
		})
		return false, nil
	}

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         source.Region,
		Phase:          "cache",
		Message:        "restoring cache from local backup",
		CurrentStep:    currentStep,
		TotalSteps:     totalSteps,
		FileCount:      len(backupPayload),
		ProcessedFiles: 0,
		TotalFiles:     len(backupPayload),
		UpdatedAt:      time.Now().UTC(),
	})

	if err := usecase.cache.StoreRegion(ctx, source.Region, backupPayload); err != nil {
		return false, fmt.Errorf("restore latest backup payload: %w", err)
	}

	usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         source.Region,
		Phase:          "cache",
		Message:        "local backup cache restore completed",
		CurrentStep:    currentStep,
		TotalSteps:     totalSteps,
		FileCount:      len(backupPayload),
		ProcessedFiles: len(backupPayload),
		TotalFiles:     len(backupPayload),
		UpdatedAt:      time.Now().UTC(),
	})

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

func (usecase *MasterDataSyncUsecase) canRestoreFromLocalBackupByVersions(ctx context.Context, source masterdata.Source, backupPayload map[string]any) (bool, string, error) {
	localVersion, localFound := versionPayloadFromBackup(source, backupPayload)
	if !localFound {
		return false, "local_versions_missing", nil
	}

	remoteVersion, remoteFound, err := usecase.loadRemoteVersionPayload(ctx, source)
	if err != nil {
		return false, "remote_versions_load_failed", err
	}
	if !remoteFound {
		return false, "remote_versions_missing", nil
	}

	matched, err := jsonValuesEqual(localVersion, remoteVersion)
	if err != nil {
		return false, "versions_compare_failed", err
	}
	if !matched {
		return false, "versions_mismatch", nil
	}

	return true, "versions_matched", nil
}

func (usecase *MasterDataSyncUsecase) loadRemoteVersionPayload(ctx context.Context, source masterdata.Source) (any, bool, error) {
	if usecase.loader == nil {
		return nil, false, errors.New("source loader is not configured")
	}

	versionSource := source
	versionSource.Path = versionsSourcePath(source)
	payload, err := usecase.loader.LoadRegion(ctx, versionSource)
	if err != nil {
		return nil, false, err
	}

	version, found := versionPayloadFromBackup(source, payload)
	if !found {
		return nil, false, nil
	}

	return version, true, nil
}

func versionsSourcePath(source masterdata.Source) string {
	trimmedPath := strings.Trim(strings.TrimSpace(source.Path), "/")
	if trimmedPath == "" {
		return "versions.json"
	}
	if strings.EqualFold(path.Base(trimmedPath), "versions.json") {
		return trimmedPath
	}

	return path.Join(trimmedPath, "versions.json")
}

func jsonValuesEqual(left any, right any) (bool, error) {
	leftBytes, err := json.Marshal(left)
	if err != nil {
		return false, err
	}

	rightBytes, err := json.Marshal(right)
	if err != nil {
		return false, err
	}

	return bytes.Equal(leftBytes, rightBytes), nil
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
	scannedPages := 0
	scannedRecords := 0
	candidateCount := 0

	for {
		records, _, err := usecase.cache.ListByPage(ctx, region, "events", page, pageSize)
		if err != nil {
			return nil, false, fmt.Errorf("list events region %s page %d: %w", region, page, err)
		}
		if len(records) == 0 {
			break
		}
		scannedPages++
		scannedRecords += len(records)

		for _, record := range records {
			startAt, endAt, ok := resolveEventTimeRange(record)
			if !ok {
				continue
			}

			if nowMillis < startAt || nowMillis > endAt {
				continue
			}
			candidateCount++

			if selected == nil || startAt > selectedStartAt {
				selected = record
				selectedStartAt = startAt
				usecase.logf(
					"current_event scan_candidate region=%s page=%d id=%s name=%q start_ms=%d start=%s end_ms=%d end=%s scanned_records=%d candidates=%d raw_times=%s",
					region,
					page,
					currentEventRecordID(record),
					currentEventRecordName(record),
					startAt,
					formatCurrentEventTimestamp(startAt),
					endAt,
					formatCurrentEventTimestamp(endAt),
					scannedRecords,
					candidateCount,
					currentEventRawTimes(record),
				)
			}
		}

		page++
	}

	if selected == nil {
		usecase.logf(
			"current_event scan_complete region=%s found=false pages=%d scanned_records=%d candidates=%d now_ms=%d now=%s",
			region,
			scannedPages,
			scannedRecords,
			candidateCount,
			nowMillis,
			formatCurrentEventTimestamp(nowMillis),
		)
		return nil, false, nil
	}

	_, selectedEndAt, _, _ := resolveEventTimeRangeDetails(selected)
	usecase.logf(
		"current_event scan_complete region=%s found=true pages=%d scanned_records=%d candidates=%d id=%s name=%q start_ms=%d start=%s end_ms=%d end=%s raw_times=%s",
		region,
		scannedPages,
		scannedRecords,
		candidateCount,
		currentEventRecordID(selected),
		currentEventRecordName(selected),
		selectedStartAt,
		formatCurrentEventTimestamp(selectedStartAt),
		selectedEndAt,
		formatCurrentEventTimestamp(selectedEndAt),
		currentEventRawTimes(selected),
	)

	return selected, true, nil
}

func isEventInTimeRange(record map[string]any, nowMillis int64) bool {
	return evaluateCurrentEventRange(record, nowMillis).InRange
}

func resolveEventTimeRange(record map[string]any) (int64, int64, bool) {
	startAt, endAt, ok, _ := resolveEventTimeRangeDetails(record)
	return startAt, endAt, ok
}

func resolveEventTimeRangeDetails(record map[string]any) (int64, int64, bool, string) {
	if record == nil {
		return 0, 0, false, "record_nil"
	}

	startAt, startFound := selectPositiveTimestamp(record, "startAt")
	if !startFound {
		return 0, 0, false, "missing_start"
	}
	endAt, endFound := selectPositiveTimestamp(record, "closedAt")
	if !endFound {
		return 0, 0, false, "missing_end"
	}

	if endAt < startAt {
		return 0, 0, false, "end_before_start"
	}

	return startAt, endAt, true, "ok"
}

type currentEventRangeEvaluation struct {
	StartAt int64
	EndAt   int64
	InRange bool
	Reason  string
}

func evaluateCurrentEventRange(record map[string]any, nowMillis int64) currentEventRangeEvaluation {
	startAt, endAt, ok, reason := resolveEventTimeRangeDetails(record)
	if !ok {
		return currentEventRangeEvaluation{
			StartAt: startAt,
			EndAt:   endAt,
			InRange: false,
			Reason:  reason,
		}
	}

	if nowMillis < startAt {
		return currentEventRangeEvaluation{
			StartAt: startAt,
			EndAt:   endAt,
			InRange: false,
			Reason:  "before_start",
		}
	}
	if nowMillis > endAt {
		return currentEventRangeEvaluation{
			StartAt: startAt,
			EndAt:   endAt,
			InRange: false,
			Reason:  "after_end",
		}
	}

	return currentEventRangeEvaluation{
		StartAt: startAt,
		EndAt:   endAt,
		InRange: true,
		Reason:  "in_range",
	}
}

func currentEventRecordID(record map[string]any) string {
	if record == nil {
		return ""
	}

	value, ok := record["id"]
	if !ok || value == nil {
		return ""
	}

	id := strings.TrimSpace(fmt.Sprint(value))
	if id == "<nil>" {
		return ""
	}

	return id
}

func currentEventRecordName(record map[string]any) string {
	if record == nil {
		return ""
	}

	value, ok := record["name"]
	if !ok || value == nil {
		return ""
	}

	name := strings.TrimSpace(fmt.Sprint(value))
	if name == "<nil>" {
		return ""
	}

	return name
}

func formatCurrentEventTimestamp(timestampMillis int64) string {
	if timestampMillis <= 0 {
		return ""
	}

	return time.UnixMilli(timestampMillis).UTC().Format(time.RFC3339)
}

func currentEventRawTimes(record map[string]any) string {
	if record == nil {
		return ""
	}

	keys := []string{
		"startAt",
		"eventOnlyComponentDisplayStartAt",
		"distributionStartAt",
		"closedAt",
		"aggregateAt",
		"distributionEndAt",
		"eventOnlyComponentDisplayEndAt",
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}

		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}

	return strings.Join(parts, ",")
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

func selectPositiveTimestamp(record map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, exists := record[key]
		if !exists {
			continue
		}

		timestamp, ok := parseTimestamp(value)
		if !ok || timestamp <= 0 {
			continue
		}

		return timestamp, true
	}

	return 0, false
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

	// On graceful-shutdown interruption never persist a terminal failed/success
	// status. The region is left in whatever recoverable running/pending state it
	// was already in, so interrupted-sync recovery can resume it. Terminal writes
	// are also skipped in the detached retry below by the same guard.
	if isTerminalSyncStatus(status.Status) && usecase.isInterrupted(ctx) {
		usecase.logf("sync status save skipped on interruption region=%s status=%s", status.Region, status.Status)
		return nil
	}

	usecase.statusMu.Lock()
	defer usecase.statusMu.Unlock()

	// Stamp the running job's lease token so the fenced status store can
	// reject this write after a takeover by a newer owner.
	status.FencingToken = usecase.currentLeaseToken.Load()

	err := usecase.statusStore.Save(ctx, status)
	if err != nil && ctx.Err() != nil {
		retryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Do not retry a terminal write that was suppressed on interruption: the
		// region must stay recoverable, not be flipped to failed/success.
		if isTerminalSyncStatus(status.Status) && usecase.isInterrupted(ctx) {
			usecase.logf("sync status save retry skipped on interruption region=%s status=%s", status.Region, status.Status)
			return nil
		}
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
