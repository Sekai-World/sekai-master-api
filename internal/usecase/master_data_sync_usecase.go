package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
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
}

// MasterDataCacheBatchReader reads several records in one round trip.
// GetByIDs and GetByCompositeKeys return records aligned with their input, with
// nil for a miss.
type MasterDataCacheBatchReader interface {
	GetByIDs(ctx context.Context, region string, entity string, ids []string) ([]map[string]any, error)
	GetByCompositeKeys(ctx context.Context, region string, entity string, keys []map[string]any) ([]map[string]any, error)
}

// MasterDataCacheIndexReader reads records through the relation indexes built
// at sync time (see masterdata.EntityIndexes).
type MasterDataCacheIndexReader interface {
	ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error)
}

// MasterDataCacheProjectionReader reads the list projections built at sync
// time (see masterdata.ProjectionFields).
type MasterDataCacheProjectionReader interface {
	LoadProjection(ctx context.Context, region string, entity string) (*masterdata.Projection, error)
}

// MasterDataCacheDerivedDataEnsurer builds relation indexes and list
// projections that are missing or out of date for data already in the cache.
type MasterDataCacheDerivedDataEnsurer interface {
	EnsureDerivedEntityData(ctx context.Context, region string) ([]string, error)
}

// ErrCompositeReadUnsupported reports a cache that cannot read composite-key
// records directly.
var ErrCompositeReadUnsupported = errors.New("master data cache does not support composite-key reads")

type MasterDataCacheSourceDigestStorer interface {
	StoreRegionWithSourceDigests(ctx context.Context, region string, payload map[string]any, fileDigests map[string]string) error
}

type MasterDataCacheEntityInspector interface {
	HasEntityRecords(ctx context.Context, region string, entity string) (bool, error)
}

// MasterDataCacheRegionDataInspector reports whether a store holds any
// records for a region. Sync uses it to confirm the region is populated
// before a shortcut skips loading the source.
type MasterDataCacheRegionDataInspector interface {
	HasRegionData(ctx context.Context, region string) (bool, error)
}

// MasterDataCacheRegionRecordCounter reports the number of stored records per
// region, for metrics.
type MasterDataCacheRegionRecordCounter interface {
	RegionRecordCounts(ctx context.Context) (map[string]int64, error)
}

// MasterDataCacheEntityPruner deletes the entities of a region that keep does
// not name; keep holds payload file paths or entity names, and returns the
// deleted entities. Sync calls it after a successful full region load with
// that load's file paths, so entity kinds the source dropped do not linger.
type MasterDataCacheEntityPruner interface {
	PruneRegionEntities(ctx context.Context, region string, keep []string) ([]string, error)
}

type MasterDataCacheVersionStorer interface {
	StoreRegionVersionPayload(ctx context.Context, region string, version any) error
}

type MasterDataCacheVersionLoader interface {
	LoadRegionVersionPayload(ctx context.Context, region string) (any, bool, error)
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

type MasterDataSyncUsecase struct {
	sources       []masterdata.Source
	loader        MasterDataSourceLoader
	cache         MasterDataCache
	statusStore   MasterDataSyncStatusStore
	publisher     MasterDataEventPublisher
	concurrency   int
	regionTimeout time.Duration
	jobTimeout    time.Duration
	statusMu      sync.Mutex
	syncRunning   atomic.Bool

	// leaseCoordinator owns cross-pod sync admission; nil keeps the
	// process-local-only behavior. currentLeaseToken carries the fencing
	// token of the lease held by this process's running job and is stamped
	// onto every status write so the fenced status store can reject stale
	// owners after a takeover.
	leaseCoordinator      MasterDataSyncLeaseCoordinator
	leaseHeartbeatTimeout time.Duration
	leaseReleaseTimeout   time.Duration
	currentLeaseToken     atomic.Int64

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
		concurrency: concurrency,
	}
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

// SyncLeaseCoordinationEnabled reports whether a cross-pod lease coordinator
// is configured for this process. Diagnostics surfaces render the explicit
// single-instance (uncoordinated) state when it is not.
func (usecase *MasterDataSyncUsecase) SyncLeaseCoordinationEnabled() bool {
	return usecase != nil && usecase.leaseCoordinator != nil
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

	// Data writes carry the claim's token too, so a store that fences them
	// rejects this job's writes once a newer owner has taken over.
	jobCtx, cancelJob := context.WithCancel(masterdata.WithFencingToken(ctx, claim.Token))
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

	previousStatuses, latestStatuses := usecase.loadStatusMap(ctx)

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
			task.latestStatus = strings.ToLower(strings.TrimSpace(latestStatuses[source.Region].Status))
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
	// latestStatus is the region's latest status before this run, while
	// previous is its latest successful status.
	latestStatus   string
	force          bool
	step           int
	totalSteps     int
	now            time.Time
	startedAt      time.Time
	resolvedCommit string
	cacheReady     bool
	recordFailure  func(region string, err error)
}

// syncRegion runs one region through the shortcut checks and the full sync
// path. Failures are reported through recordFailure.
func (task *regionSyncTask) syncRegion(ctx, regionCtx context.Context) {
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

// ensureCacheReady checks whether the store holds the region and records a
// failure when the check itself errors.
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
// the last successful sync and whose data the store still holds.
func (task *regionSyncTask) trySkipUnchangedCommit(ctx, regionCtx context.Context) bool {
	if task.hasPreviousStatus && strings.EqualFold(strings.TrimSpace(task.previous.Status), "success") && task.previous.SourceCommit != "" && task.previous.SourceCommit == task.resolvedCommit {
		return task.trySkipWhenStoreHasRegion(ctx, regionCtx)
	}
	return false
}

// trySkipWhenStoreHasRegion skips the sync when the store still holds the
// region, its derived data is current, and its version payload is stored. It
// reports whether the region was skipped.
func (task *regionSyncTask) trySkipWhenStoreHasRegion(ctx, regionCtx context.Context) bool {
	inspector, ok := task.usecase.cache.(MasterDataCacheRegionDataInspector)
	if !ok {
		return false
	}
	populated, checkErr := inspector.HasRegionData(regionCtx, task.source.Region)
	if checkErr != nil {
		task.usecase.logf("sync compare region=%s commit=%s store_check=failed error=%v", task.source.Region, task.resolvedCommit, checkErr)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but the stored region could not be checked, fallback to full sync", task.now)
		return false
	}
	if !populated {
		task.usecase.logf("sync compare region=%s commit=%s store=empty fallback=full_sync", task.source.Region, task.resolvedCommit)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but the store has no data for the region, fallback to full sync", task.now)
		return false
	}
	if ensurer, ok := task.usecase.cache.(MasterDataCacheDerivedDataEnsurer); ok {
		indexed, ensureErr := ensurer.EnsureDerivedEntityData(regionCtx, task.source.Region)
		if ensureErr != nil {
			task.usecase.logf("sync compare region=%s commit=%s derived_data_ensure=failed error=%v fallback=full_sync", task.source.Region, task.resolvedCommit, ensureErr)
			task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but derived data build failed, fallback to full sync", task.now)
			return false
		}
		if len(indexed) > 0 {
			task.usecase.logf("sync compare region=%s commit=%s derived_data_built=%v", task.source.Region, task.resolvedCommit, indexed)
		}
	}
	if !task.usecase.versionPayloadStored(regionCtx, task.source) {
		task.usecase.logf("sync compare region=%s commit=%s version_cache=missing fallback=full_sync", task.source.Region, task.resolvedCommit)
		task.publishRegionProgress(ctx, "running", "compare", "commit unchanged but version cache unavailable, fallback to full sync", task.now)
		return false
	}

	task.usecase.logf("sync skipped region=%s reason=commit_unchanged commit=%s check=region_data_present", task.source.Region, task.resolvedCommit)
	task.publishRegionProgress(ctx, "success", "compare", "commit unchanged, stored region data present and skipped sync", task.now)
	return task.persistUnchangedSkipStatus(ctx)
}

// trySkipChangedCommit keeps the stored data when the remote commit changed
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

// runRegionFullSync loads the region payload from the source, stores it, and
// persists the success status.
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
	if task.pruneRemovedEntities(ctx, regionCtx, payload) {
		return
	}
	if task.storeRegionVersionPayload(ctx, regionCtx, payload) {
		return
	}
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
	// The rate-limit fallback keeps the stored data as the previous success,
	// so it applies only while the store holds the region and the last run
	// succeeded: after a failed or interrupted run the store may hold a mix
	// of two commits.
	if isRateLimitError(loadErr) && task.cacheReady && task.latestStatus == "success" {
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

// storeRegionPayload writes the payload to the store, preferring the
// digest-aware store path when the store supports it. It reports whether the region failed.
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

// pruneRemovedEntities deletes the region's stored entities that the full
// payload no longer has, when the store supports it. It reports whether the
// region failed.
func (task *regionSyncTask) pruneRemovedEntities(ctx, regionCtx context.Context, payload map[string]any) bool {
	pruner, ok := task.usecase.cache.(MasterDataCacheEntityPruner)
	if !ok || len(payload) == 0 {
		return false
	}

	keep := make([]string, 0, len(payload))
	for filePath := range payload {
		keep = append(keep, filePath)
	}
	removed, err := pruner.PruneRegionEntities(regionCtx, task.source.Region, keep)
	if err == nil {
		if len(removed) > 0 {
			task.usecase.logf("sync pruned entities region=%s entities=%v", task.source.Region, removed)
		}
		return false
	}

	duration := time.Since(task.startedAt).Milliseconds()
	task.usecase.logf("sync failed region=%s phase=prune duration_ms=%d error=%v", task.source.Region, duration, err)
	task.usecase.publishSyncEvent(ctx, masterdata.SyncUpdatedEvent{
		Event:       "master_data_sync_progress",
		Status:      "failed",
		Region:      task.source.Region,
		Phase:       "cache",
		Message:     err.Error(),
		CurrentStep: task.step,
		TotalSteps:  task.totalSteps,
		FileCount:   len(payload),
		DurationMS:  duration,
		UpdatedAt:   time.Now().UTC(),
	})
	task.recordFailure(task.source.Region, err)
	task.persistFailedStatus(ctx, len(payload), duration, err.Error())
	return true
}

// storeRegionVersionPayload stores the versions payload found in the loaded
// files as the region's version payload. It reports whether the region failed.
func (task *regionSyncTask) storeRegionVersionPayload(ctx, regionCtx context.Context, payload map[string]any) bool {
	versionStore, ok := task.usecase.cache.(MasterDataCacheVersionStorer)
	if !ok {
		return false
	}

	versionPayload, versionFound := versionPayloadFromFiles(task.source, payload)
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

// trySkipRegionWithUnchangedManifest keeps the stored data when the commit
// changed but the source's versions manifest equals the stored version
// payload, so the new commit changed nothing the store holds. Relation
// indexes and list projections whose definitions changed are rebuilt from the
// stored records first. It reports whether the region was skipped.
func (usecase *MasterDataSyncUsecase) trySkipRegionWithUnchangedManifest(ctx context.Context, source masterdata.Source, previous masterdata.SyncStatus, resolvedCommit string, cacheReady bool, progress manifestSkipProgress) (bool, error) {
	if !cacheReady || !usecase.versionManifestsMatchForSkip(ctx, source, resolvedCommit) {
		return false, nil
	}
	if ensurer, ok := usecase.cache.(MasterDataCacheDerivedDataEnsurer); ok {
		built, err := ensurer.EnsureDerivedEntityData(ctx, source.Region)
		if err != nil {
			usecase.logf("sync compare region=%s commit=%s derived_data_ensure=failed error=%v fallback=full_sync", source.Region, resolvedCommit, err)
			return false, nil
		}
		if len(built) > 0 {
			usecase.logf("sync compare region=%s commit=%s derived_data_built=%v", source.Region, resolvedCommit, built)
		}
	}

	skippedAt := time.Now().UTC()
	if err := usecase.saveStatus(ctx, masterdata.SyncStatus{
		Region:         previous.Region,
		Status:         "success",
		FileCount:      previous.FileCount,
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
		Message:     "versions manifest unchanged, kept stored data and skipped sync",
		CurrentStep: progress.currentStep,
		TotalSteps:  progress.totalSteps,
		FileCount:   previous.FileCount,
		UpdatedAt:   progress.updatedAt,
	})

	return true, nil
}

func (usecase *MasterDataSyncUsecase) versionManifestsMatchForSkip(ctx context.Context, source masterdata.Source, resolvedCommit string) bool {
	manifestLoader, ok := usecase.loader.(MasterDataSourceVersionManifestLoader)
	if !ok {
		return false
	}

	versionLoader, ok := usecase.cache.(MasterDataCacheVersionLoader)
	if !ok {
		return false
	}

	localManifest, localFound, err := versionLoader.LoadRegionVersionPayload(ctx, source.Region)
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

// versionPayloadStored reports whether the region's version payload is
// stored, or true when the store keeps no separate version payload. A missing
// payload makes the caller fall back to a full sync.
func (usecase *MasterDataSyncUsecase) versionPayloadStored(ctx context.Context, source masterdata.Source) bool {
	versionLoader, ok := usecase.cache.(MasterDataCacheVersionLoader)
	if !ok {
		return true
	}
	_, found, loadErr := versionLoader.LoadRegionVersionPayload(ctx, source.Region)
	return loadErr == nil && found
}

// loadStatusMap returns each region's latest successful status, falling back
// to its latest status when it has never succeeded, and each region's latest
// status.
func (usecase *MasterDataSyncUsecase) loadStatusMap(ctx context.Context) (map[string]masterdata.SyncStatus, map[string]masterdata.SyncStatus) {
	statusMap := make(map[string]masterdata.SyncStatus)
	latestMap := make(map[string]masterdata.SyncStatus)
	if usecase.statusStore == nil {
		return statusMap, latestMap
	}

	statuses, err := usecase.statusStore.List(ctx)
	if err != nil {
		usecase.logf("load previous statuses failed error=%v", err)
		return statusMap, latestMap
	}

	for _, status := range statuses {
		if strings.TrimSpace(status.Region) == "" {
			continue
		}
		statusMap[status.Region] = status
		latestMap[status.Region] = status
	}

	if successStore, ok := usecase.statusStore.(MasterDataSyncLatestSuccessStore); ok {
		successStatuses, successErr := successStore.ListLatestSuccess(ctx)
		if successErr != nil {
			usecase.logf("load latest successful statuses failed error=%v", successErr)
			return statusMap, latestMap
		}

		for _, status := range successStatuses {
			if strings.TrimSpace(status.Region) == "" {
				continue
			}
			statusMap[status.Region] = status
		}
	}

	return statusMap, latestMap
}

func (usecase *MasterDataSyncUsecase) Status(ctx context.Context) ([]masterdata.SyncStatus, error) {
	if usecase.statusStore == nil {
		return nil, nil
	}

	return usecase.statusStore.List(ctx)
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

// HasRegionData reports whether the store holds any records for region. A
// store that cannot tell reports false.
func (usecase *MasterDataSyncUsecase) HasRegionData(ctx context.Context, region string) (bool, error) {
	if usecase == nil || usecase.cache == nil {
		return false, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false, nil
	}

	if inspector, ok := usecase.cache.(MasterDataCacheRegionDataInspector); ok {
		return inspector.HasRegionData(ctx, region)
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

func (usecase *MasterDataSyncUsecase) regionCacheReady(ctx context.Context, region string) (bool, error) {
	if usecase == nil || usecase.cache == nil {
		return true, nil
	}

	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return false, nil
	}

	inspector, ok := usecase.cache.(MasterDataCacheRegionDataInspector)
	if !ok {
		return true, nil
	}
	hasData, err := inspector.HasRegionData(ctx, region)
	if err != nil {
		return false, fmt.Errorf("check region data %s: %w", region, err)
	}
	return hasData, nil
}

// RegionRecordCounts returns the number of stored records per region, or nil
// when the store cannot count them. It only reads.
func (usecase *MasterDataSyncUsecase) RegionRecordCounts(ctx context.Context) (map[string]int64, error) {
	if usecase == nil {
		return nil, nil
	}
	counter, ok := usecase.cache.(MasterDataCacheRegionRecordCounter)
	if !ok {
		return nil, nil
	}
	return counter.RegionRecordCounts(ctx)
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

// RegionVersionReady reports whether usable version metadata for a region is
// stored. The serve readiness probe requires persisted card
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
		// caller as master_data), not a store failure. Only genuine read failures
		// propagate as errors, so the readiness probe reports the database
		// dependency instead of master_data.
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

	loader, ok := usecase.cache.(MasterDataCacheVersionLoader)
	if !ok {
		return nil, false, nil
	}
	version, found, err := loader.LoadRegionVersionPayload(ctx, source.Region)
	if err != nil {
		return nil, false, fmt.Errorf("load region version payload %s: %w", source.Region, err)
	}
	if version == nil || !found {
		return nil, false, nil
	}
	return version, true, nil
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

func versionPayloadFromFiles(source masterdata.Source, payload map[string]any) (any, bool) {
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

// GetByIDs returns the records stored under ids, aligned with ids, with nil for
// a miss. A cache without batch reads is read one ID at a time.
func (usecase *MasterDataSyncUsecase) GetByIDs(ctx context.Context, region string, entity string, ids []string) ([]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.get_by_ids", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))), attribute.Int("request.count", len(ids)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return make([]map[string]any, len(ids)), nil
	}
	if reader, ok := usecase.cache.(MasterDataCacheBatchReader); ok {
		var records []map[string]any
		records, err = reader.GetByIDs(ctx, region, entity, ids)
		return records, err
	}

	records := make([]map[string]any, len(ids))
	for index, id := range ids {
		var record map[string]any
		var found bool
		record, found, err = usecase.cache.GetByID(ctx, region, entity, id)
		if err != nil {
			return nil, err
		}
		if found {
			records[index] = record
		}
	}
	return records, nil
}

// ListByIndex returns, for each lookup, the records whose indexed fields equal
// the lookup's values (in the index's field order), in stored order. A cache
// without index reads is scanned, which only test doubles rely on.
func (usecase *MasterDataSyncUsecase) ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.list_by_index", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))), attribute.String("index", index), attribute.Int("request.count", len(lookups)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	results := make([][]map[string]any, len(lookups))
	if usecase.cache == nil {
		return results, nil
	}
	if reader, ok := usecase.cache.(MasterDataCacheIndexReader); ok {
		results, err = reader.ListByIndex(ctx, region, entity, index, lookups)
		return results, err
	}

	var records []map[string]any
	records, err = usecase.cache.ListAll(ctx, region, entity)
	if err != nil {
		return nil, err
	}
	for position, lookup := range lookups {
		results[position] = make([]map[string]any, 0)
		key, ok := masterdata.IndexLookupKey(lookup...)
		if !ok {
			continue
		}
		for _, record := range records {
			if slices.Contains(masterdata.IndexKeys(record, index), key) {
				results[position] = append(results[position], record)
			}
		}
	}
	return results, nil
}

// LoadProjection returns entity's list projection: the fields list endpoints
// filter, sort, and return, for every record, by column. A cache without
// projections has one built from its records, which only test doubles rely on.
func (usecase *MasterDataSyncUsecase) LoadProjection(ctx context.Context, region string, entity string) (*masterdata.Projection, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.load_projection", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return masterdata.BuildProjection(entity, nil, nil), nil
	}
	if reader, ok := usecase.cache.(MasterDataCacheProjectionReader); ok {
		var projection *masterdata.Projection
		projection, err = reader.LoadProjection(ctx, region, entity)
		return projection, err
	}

	var records []map[string]any
	records, err = usecase.cache.ListAll(ctx, region, entity)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(records))
	for position, record := range records {
		keys[position], _ = masterdata.CanonicalKeyPart(record["id"])
	}
	return masterdata.BuildProjection(entity, keys, records), nil
}

// GetByCompositeKeys returns the records of a composite-key entity (such as
// resourceboxes keyed by id and resourceBoxPurpose) matching each key, aligned
// with keys, with nil for a miss.
func (usecase *MasterDataSyncUsecase) GetByCompositeKeys(ctx context.Context, region string, entity string, keys []map[string]any) ([]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "master_data.get_by_composite_keys", attribute.String("region", strings.ToLower(strings.TrimSpace(region))), attribute.String("entity", strings.ToLower(strings.TrimSpace(entity))), attribute.Int("request.count", len(keys)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if usecase.cache == nil {
		return make([]map[string]any, len(keys)), nil
	}
	reader, ok := usecase.cache.(MasterDataCacheBatchReader)
	if !ok {
		err = ErrCompositeReadUnsupported
		return nil, err
	}

	var records []map[string]any
	records, err = reader.GetByCompositeKeys(ctx, region, entity, keys)
	return records, err
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

// CurrentEvent returns the event whose startAt..closedAt window contains now,
// preferring the latest start. It picks the event from the events list
// projection and reads only that record, so it never writes.
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

	projection, err := usecase.LoadProjection(ctx, normalizedRegion, "events")
	if err != nil {
		return nil, false, fmt.Errorf("load events projection region %s: %w", normalizedRegion, err)
	}

	selectedRow := -1
	selectedStartAt := int64(0)
	for row := 0; row < projection.Len(); row++ {
		startAt, endAt, ok := resolveEventTimeRange(projection.Row(row))
		if !ok || nowMillis < startAt || nowMillis > endAt {
			continue
		}
		if selectedRow < 0 || startAt > selectedStartAt {
			selectedRow = row
			selectedStartAt = startAt
		}
	}
	if selectedRow < 0 {
		return nil, false, nil
	}

	key := projection.Keys[selectedRow]
	record, found, err := usecase.GetByID(ctx, normalizedRegion, "events", key)
	if err != nil {
		return nil, false, fmt.Errorf("get current event region %s id %s: %w", normalizedRegion, key, err)
	}
	if !found {
		usecase.logf("current_event record_missing region=%s id=%s start=%s", normalizedRegion, key, formatCurrentEventTimestamp(selectedStartAt))
		return nil, false, nil
	}
	return record, true, nil
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

	commit := strings.TrimSpace(previous.SourceCommit)

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

	usecase.logf("fallback kept previous stored state region=%s commit=%s", source.Region, commit)

	return nil
}

func resolveEventTimeRange(record map[string]any) (int64, int64, bool) {
	if record == nil {
		return 0, 0, false
	}

	startAt, startFound := selectPositiveTimestamp(record, "startAt")
	if !startFound {
		return 0, 0, false
	}
	endAt, endFound := selectPositiveTimestamp(record, "closedAt")
	if !endFound {
		return 0, 0, false
	}

	if endAt < startAt {
		return 0, 0, false
	}

	return startAt, endAt, true
}

func formatCurrentEventTimestamp(timestampMillis int64) string {
	if timestampMillis <= 0 {
		return ""
	}

	return time.UnixMilli(timestampMillis).UTC().Format(time.RFC3339)
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
