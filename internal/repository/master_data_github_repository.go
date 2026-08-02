package repository

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/observability"
	"sekai-master-api/internal/tracing"
)

type GitHubMasterDataRepository struct {
	httpClient      *http.Client
	token           string
	fileConcurrency int
	retryCount      int
	retryBackoff    time.Duration
	apiBaseURL      string
	gitBaseURL      string
	rawBaseURL      string
	resumeBaseDir   string
}

const defaultGitHubAPIBaseURL = "https://api.github.com"
const defaultGitHubBaseURL = "https://github.com"
const defaultGitHubRawBaseURL = "https://raw.githubusercontent.com"
const defaultMasterDataResumeBaseDir = "tmp/master-data-sync-resume"

const (
	githubUserAgentHeader     = "User-Agent"
	githubUserAgentValue      = "sekai-master-api/1.0"
	githubAuthorizationHeader = "Authorization"
	githubBearerPrefix        = "Bearer "
	versionManifestFileName   = "versions.json"

	gitUploadPackAdvertisementMediaType = "application/x-git-upload-pack-advertisement"
	gitUploadPackServiceAdvertisement   = "# service=git-upload-pack\n"
)

type gitCommitResponse struct {
	SHA string `json:"sha"`
}

const maxArchiveJSONFileSize = 64 << 20

func NewGitHubMasterDataRepository(timeout time.Duration, token string, fileConcurrency int, retryCount int, retryBackoff time.Duration) *GitHubMasterDataRepository {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if fileConcurrency <= 0 {
		fileConcurrency = 8
	}
	if retryCount <= 0 {
		retryCount = 3
	}
	if retryBackoff <= 0 {
		retryBackoff = 300 * time.Millisecond
	}

	return &GitHubMasterDataRepository{
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: observability.NewHTTPTransport(http.DefaultTransport, "github-master-data"),
		},
		token:           strings.TrimSpace(token),
		fileConcurrency: fileConcurrency,
		retryCount:      retryCount,
		retryBackoff:    retryBackoff,
		apiBaseURL:      defaultGitHubAPIBaseURL,
		rawBaseURL:      defaultGitHubRawBaseURL,
		resumeBaseDir:   defaultMasterDataResumeBaseDir,
	}
}

func (repository *GitHubMasterDataRepository) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "github.master_data.load_region", attribute.String("region", strings.ToLower(strings.TrimSpace(source.Region))), attribute.String("github.owner", source.Owner), attribute.String("github.repo", source.Repo))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	archiveURL := repository.tarballURL(source)
	workspaceDir := repository.resumeDir(source)
	archivePath := filepath.Join(workspaceDir, "source.tar.gz")

	if removeErr := os.RemoveAll(workspaceDir); removeErr != nil {
		err = fmt.Errorf("clear archive workspace for region %s: %w", source.Region, removeErr)
		return nil, err
	}
	if mkdirErr := os.MkdirAll(workspaceDir, 0o755); mkdirErr != nil {
		err = fmt.Errorf("create archive workspace for region %s: %w", source.Region, mkdirErr)
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(workspaceDir); err != nil {
			logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=archive status=cleanup_failed error=%v", source.Region, err))
		}
	}()

	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:     "master_data_sync_progress",
		Status:    "running",
		Region:    source.Region,
		Phase:     "load_archive",
		Message:   "downloading source archive",
		UpdatedAt: time.Now().UTC(),
	})

	if downloadErr := repository.downloadArchiveToFile(ctx, archiveURL, archivePath); downloadErr != nil {
		err = fmt.Errorf("download archive for region %s: %w", source.Region, downloadErr)
		return nil, err
	}

	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:     "master_data_sync_progress",
		Status:    "running",
		Region:    source.Region,
		Phase:     "load_extract",
		Message:   "extracting source archive",
		UpdatedAt: time.Now().UTC(),
	})

	files, err := repository.extractArchivePayload(ctx, archivePath, source)
	if err != nil {
		err = fmt.Errorf("extract archive for region %s: %w", source.Region, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("file.count", len(files)))

	logging.InfoKV("master-data-loader", fmt.Sprintf("region=%s phase=load status=success files=%d", source.Region, len(files)))
	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:      "master_data_sync_progress",
		Status:     "running",
		Region:     source.Region,
		Phase:      "load_done",
		Message:    "region archive loading completed",
		TotalFiles: len(files),
		FileCount:  len(files),
		UpdatedAt:  time.Now().UTC(),
	})

	return files, nil
}

func (repository *GitHubMasterDataRepository) ResolveRegionVersion(ctx context.Context, source masterdata.Source) (string, error) {
	ctx, span := tracing.StartSpan(ctx, "github.master_data.resolve_region_version", attribute.String("region", strings.ToLower(strings.TrimSpace(source.Region))), attribute.String("github.owner", source.Owner), attribute.String("github.repo", source.Repo))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	commitURL := fmt.Sprintf(
		"%s/repos/%s/%s/commits/%s",
		strings.TrimRight(repository.apiBaseURL, "/"),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
		url.PathEscape(source.Ref),
	)

	var commitResp gitCommitResponse
	if getErr := repository.getJSON(ctx, commitURL, &commitResp); getErr != nil {
		if smartSHA, smartErr := repository.resolveRegionVersionFromGitSmartHTTP(ctx, source); smartErr == nil {
			return smartSHA, nil
		} else {
			err = fmt.Errorf("resolve commit for region %s: %w (smart HTTP fallback failed: %v)", source.Region, getErr, smartErr)
			return "", err
		}
	}

	return strings.TrimSpace(commitResp.SHA), nil
}

func (repository *GitHubMasterDataRepository) loadVersionManifest(ctx context.Context, source masterdata.Source) (map[string]any, bool, error) {
	manifestURL := fmt.Sprintf(
		"%s/%s",
		strings.TrimRight(repository.rawContentBaseURL(), "/"),
		escapeRawPath(strings.Join([]string{source.Owner, source.Repo, source.Ref, versionManifestPath(source.Path)}, "/")),
	)

	var manifest map[string]any
	if err := repository.getJSON(ctx, manifestURL, &manifest); err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusNotFound {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("load versions manifest for region %s: %w", source.Region, err)
	}

	return manifest, true, nil
}

func (repository *GitHubMasterDataRepository) LoadVersionManifest(ctx context.Context, source masterdata.Source) (map[string]any, bool, error) {
	return repository.loadVersionManifest(ctx, source)
}

func (repository *GitHubMasterDataRepository) rawContentBaseURL() string {
	if baseURL := strings.TrimRight(strings.TrimSpace(repository.rawBaseURL), "/"); baseURL != "" {
		return baseURL
	}

	return defaultGitHubRawBaseURL
}

func versionManifestPath(sourcePath string) string {
	trimmedPath := strings.Trim(strings.TrimSpace(sourcePath), "/")
	if trimmedPath == "" {
		return versionManifestFileName
	}
	if strings.EqualFold(path.Base(trimmedPath), versionManifestFileName) {
		return trimmedPath
	}

	return path.Join(trimmedPath, versionManifestFileName)
}

func escapeRawPath(rawPath string) string {
	parts := strings.Split(strings.Trim(rawPath, "/"), "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}

	return strings.Join(escaped, "/")
}

func (repository *GitHubMasterDataRepository) resolveRegionVersionFromGitSmartHTTP(ctx context.Context, source masterdata.Source) (string, error) {
	infoRefsURL := fmt.Sprintf(
		"%s/%s/%s.git/info/refs?service=git-upload-pack",
		repository.gitInfoRefsBaseURL(),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
	)

	refs, err := repository.getGitUploadPackRefs(ctx, infoRefsURL)
	if err != nil {
		return "", err
	}

	return selectGitUploadPackRef(refs, source.Ref)
}

func (repository *GitHubMasterDataRepository) gitInfoRefsBaseURL() string {
	if baseURL := strings.TrimRight(strings.TrimSpace(repository.gitBaseURL), "/"); baseURL != "" {
		return baseURL
	}

	return defaultGitHubBaseURL
}

func (repository *GitHubMasterDataRepository) getGitUploadPackRefs(ctx context.Context, targetURL string) (map[string]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build smart HTTP request: %w", err)
	}

	request.Header.Set("Accept", "application/x-git-upload-pack-advertisement")
	repository.applyRequestHeaders(request)

	resp, err := repository.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("perform smart HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read smart HTTP response body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &httpStatusError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	if err := validateGitUploadPackResponse(resp.Header.Get("Content-Type"), body); err != nil {
		return nil, err
	}

	return parseGitUploadPackRefs(body)
}

type gitPktLineKind uint8

const (
	gitPktLineData gitPktLineKind = iota
	gitPktLineFlush
	gitPktLineResponseEnd
	gitPktLineDelimiter
)

type gitPktLineReader struct {
	body   []byte
	offset int
}

func (reader *gitPktLineReader) read() ([]byte, gitPktLineKind, error) {
	if len(reader.body)-reader.offset < 4 {
		return nil, gitPktLineData, fmt.Errorf("truncated git pkt-line header")
	}

	packetLength, err := strconv.ParseUint(string(reader.body[reader.offset:reader.offset+4]), 16, 16)
	if err != nil {
		return nil, gitPktLineData, fmt.Errorf("parse git pkt-line length: %w", err)
	}
	reader.offset += 4

	switch packetLength {
	case 0:
		return nil, gitPktLineFlush, nil
	case 1:
		return nil, gitPktLineResponseEnd, nil
	case 2:
		return nil, gitPktLineDelimiter, nil
	case 3:
		return nil, gitPktLineData, fmt.Errorf("invalid git pkt-line length %04x", packetLength)
	}

	if packetLength < 4 {
		return nil, gitPktLineData, fmt.Errorf("invalid git pkt-line length %04x", packetLength)
	}
	packetPayloadLength := int(packetLength) - 4
	if packetPayloadLength > len(reader.body)-reader.offset {
		return nil, gitPktLineData, fmt.Errorf("truncated git pkt-line payload")
	}

	payload := reader.body[reader.offset : reader.offset+packetPayloadLength]
	reader.offset += packetPayloadLength
	return payload, gitPktLineData, nil
}

func validateGitUploadPackResponse(contentType string, body []byte) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("parse git smart HTTP response content type %q: %w", contentType, err)
	}
	if mediaType != gitUploadPackAdvertisementMediaType {
		return fmt.Errorf("unexpected git smart HTTP response content type %q, want %q", mediaType, gitUploadPackAdvertisementMediaType)
	}
	if !hasGitPktLineServicePrefix(body) {
		return fmt.Errorf("git smart HTTP response does not begin with a valid pkt-line service advertisement")
	}

	reader := gitPktLineReader{body: body}
	payload, kind, err := reader.read()
	if err != nil {
		return fmt.Errorf("read git smart HTTP service advertisement: %w", err)
	}
	if kind != gitPktLineData || string(payload) != gitUploadPackServiceAdvertisement {
		return fmt.Errorf("git smart HTTP response first pkt-line payload was %q, want %q", payload, gitUploadPackServiceAdvertisement)
	}

	return nil
}

func hasGitPktLineServicePrefix(body []byte) bool {
	if len(body) < 5 || body[4] != '#' {
		return false
	}

	for _, char := range body[:4] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}

	return true
}

func parseGitUploadPackRefs(body []byte) (map[string]string, error) {
	refs := make(map[string]string)
	reader := gitPktLineReader{body: body}
	for reader.offset < len(body) {
		payload, kind, err := reader.read()
		if err != nil {
			return nil, err
		}
		if kind != gitPktLineData {
			continue
		}

		packet := strings.TrimSuffix(string(payload), "\n")

		if strings.HasPrefix(packet, "# service=") || packet == "version 2" {
			continue
		}

		separator := strings.IndexByte(packet, ' ')
		if separator <= 0 {
			return nil, fmt.Errorf("invalid git ref advertisement packet")
		}

		sha := strings.TrimSpace(packet[:separator])
		refName := packet[separator+1:]
		if capabilitiesSeparator := strings.IndexByte(refName, '\x00'); capabilitiesSeparator >= 0 {
			refName = refName[:capabilitiesSeparator]
		}
		refName = strings.TrimSpace(refName)
		if sha == "" || refName == "" {
			return nil, fmt.Errorf("invalid git ref advertisement packet")
		}

		refs[refName] = sha
	}

	if len(refs) == 0 {
		return nil, fmt.Errorf("git smart HTTP response contained no refs")
	}

	return refs, nil
}

func selectGitUploadPackRef(refs map[string]string, requestedRef string) (string, error) {
	requestedRef = strings.TrimSpace(requestedRef)
	candidates := make([]string, 0, 3)
	addCandidate := func(candidate string) {
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		candidates = append(candidates, candidate)
	}

	if requestedRef == "" || requestedRef == "HEAD" {
		addCandidate("HEAD")
	} else {
		addCandidate(requestedRef)
		if !strings.HasPrefix(requestedRef, "refs/") {
			addCandidate("refs/heads/" + requestedRef)
			addCandidate("refs/tags/" + requestedRef)
		}
	}

	for _, candidate := range candidates {
		if sha, ok := refs[candidate]; ok {
			return strings.TrimSpace(sha), nil
		}
	}

	return "", fmt.Errorf("git smart HTTP response did not contain ref %q", requestedRef)
}

func (repository *GitHubMasterDataRepository) reportProgress(ctx context.Context, event masterdata.SyncUpdatedEvent) {
	reporter := masterdata.ProgressReporterFromContext(ctx)
	if reporter == nil {
		return
	}

	reporter(event)
}

func (repository *GitHubMasterDataRepository) tarballURL(source masterdata.Source) string {
	return fmt.Sprintf(
		"%s/repos/%s/%s/tarball/%s",
		strings.TrimRight(repository.apiBaseURL, "/"),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
		url.PathEscape(source.Ref),
	)
}

func (repository *GitHubMasterDataRepository) resumeDir(source masterdata.Source) string {
	hasher := sha1.New()
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(source.Region))))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Owner)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Repo)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Ref)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Path)))

	resumeKey := hex.EncodeToString(hasher.Sum(nil))
	regionKey := strings.ToLower(strings.TrimSpace(source.Region))
	if regionKey == "" {
		regionKey = "default"
	}

	return filepath.Join(repository.resumeBaseDir, regionKey, resumeKey)
}

func (repository *GitHubMasterDataRepository) downloadArchiveToFile(ctx context.Context, archiveURL string, targetPath string) error {
	ctx, span := tracing.StartSpan(ctx, "github.master_data.download_archive")
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkdirErr != nil {
		err = fmt.Errorf("create archive directory: %w", mkdirErr)
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= repository.retryCount; attempt++ {
		if err := repository.streamArchiveToFile(ctx, archiveURL, targetPath); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if !isRetriableRequestError(lastErr) || attempt >= repository.retryCount {
			break
		}

		if sleepErr := repository.waitRetryBackoff(ctx, attempt); sleepErr != nil {
			return sleepErr
		}
	}

	err = lastErr
	return err
}

func (repository *GitHubMasterDataRepository) extractArchivePayload(ctx context.Context, archivePath string, source masterdata.Source) (map[string]any, error) {
	_, span := tracing.StartSpan(ctx, "github.master_data.extract_archive", attribute.String("region", strings.ToLower(strings.TrimSpace(source.Region))))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive file: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	files := make(map[string]any)
	resultMu := sync.Mutex{}
	var wg sync.WaitGroup
	decodeConcurrency := repository.fileConcurrency
	if decodeConcurrency <= 0 {
		decodeConcurrency = 1
	}
	semaphore := make(chan struct{}, decodeConcurrency)
	var decodeErr error

	recordDecodeErr := func(err error) {
		resultMu.Lock()
		if decodeErr == nil {
			decodeErr = err
		}
		resultMu.Unlock()
	}

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive entry: %w", err)
		}

		if header.FileInfo().IsDir() || !header.FileInfo().Mode().IsRegular() {
			continue
		}

		relativePath, ok := archiveRelativeJSONPath(header.Name, source.Path)
		if !ok {
			continue
		}

		if header.Size < 0 {
			return nil, fmt.Errorf("invalid archive file size for %s", relativePath)
		}
		if header.Size > maxArchiveJSONFileSize {
			return nil, fmt.Errorf("archive file %s exceeds size limit", relativePath)
		}

		body, err := io.ReadAll(io.LimitReader(tarReader, header.Size))
		if err != nil {
			return nil, fmt.Errorf("read archive file %s: %w", relativePath, err)
		}

		semaphore <- struct{}{}
		wg.Go(func() {
			defer func() {
				<-semaphore
			}()

			var parsed any
			if err := json.Unmarshal(body, &parsed); err != nil {
				recordDecodeErr(fmt.Errorf("decode archive file %s: %w", relativePath, err))
				return
			}

			resultMu.Lock()
			files[relativePath] = parsed
			resultMu.Unlock()
		})
	}
	wg.Wait()

	if decodeErr != nil {
		return nil, decodeErr
	}

	span.SetAttributes(attribute.Int("file.count", len(files)))
	return files, nil
}

func archiveRelativeJSONPath(entryName string, basePath string) (string, bool) {
	trimmedEntryName := strings.Trim(strings.TrimSpace(entryName), "/")
	if trimmedEntryName == "" {
		return "", false
	}

	parts := strings.SplitN(trimmedEntryName, "/", 2)
	if len(parts) < 2 {
		return "", false
	}

	relativePath := strings.Trim(parts[1], "/")
	if relativePath == "" {
		return "", false
	}
	if hasPathTraversalSegment(relativePath) {
		return "", false
	}

	cleanPath := strings.TrimPrefix(path.Clean("/"+relativePath), "/")
	if cleanPath == "" || cleanPath == "." || strings.HasPrefix(cleanPath, "../") {
		return "", false
	}
	if !strings.HasSuffix(strings.ToLower(cleanPath), ".json") {
		return "", false
	}

	normalizedBasePath := strings.Trim(strings.TrimSpace(basePath), "/")
	if normalizedBasePath != "" && cleanPath != normalizedBasePath && !strings.HasPrefix(cleanPath, normalizedBasePath+"/") {
		return "", false
	}

	return cleanPath, true
}

func hasPathTraversalSegment(relativePath string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(relativePath, "\\", "/"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func (repository *GitHubMasterDataRepository) streamArchiveToFile(ctx context.Context, targetURL string, targetPath string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	repository.applyRequestHeaders(request)

	resp, err := repository.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("read error response body: %w", readErr)
		}
		return &httpStatusError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp archive file: %w", err)
	}

	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		_ = tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		return fmt.Errorf("write archive file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync archive file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close archive file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("move archive file into place: %w", err)
	}

	cleanupTemp = false
	return nil
}

func (repository *GitHubMasterDataRepository) getJSON(ctx context.Context, targetURL string, out any) error {
	var lastErr error
	for attempt := 1; attempt <= repository.retryCount; attempt++ {
		err := repository.doJSONRequest(ctx, targetURL, out)
		if err == nil {
			return nil
		}

		lastErr = err
		if !isRetriableRequestError(err) || attempt >= repository.retryCount {
			break
		}

		if sleepErr := repository.waitRetryBackoff(ctx, attempt); sleepErr != nil {
			return sleepErr
		}
	}

	return lastErr
}

func (repository *GitHubMasterDataRepository) doJSONRequest(ctx context.Context, targetURL string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	repository.applyRequestHeaders(request)

	resp, err := repository.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return &httpStatusError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json response: %w", err)
	}

	return nil
}

func (repository *GitHubMasterDataRepository) applyRequestHeaders(request *http.Request) {
	request.Header.Set(githubUserAgentHeader, githubUserAgentValue)
	if repository.token != "" {
		request.Header.Set(githubAuthorizationHeader, githubBearerPrefix+repository.token)
	}
}

func (repository *GitHubMasterDataRepository) waitRetryBackoff(ctx context.Context, attempt int) error {
	backoff := repository.retryBackoff * time.Duration(attempt)
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type httpStatusError struct {
	statusCode int
	body       string
}

func (err *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", err.statusCode, err.body)
}

func isRetriableRequestError(err error) bool {
	if err == nil {
		return false
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		if statusErr.statusCode == http.StatusTooManyRequests {
			return true
		}

		return statusErr.statusCode >= http.StatusInternalServerError
	}

	return true
}
