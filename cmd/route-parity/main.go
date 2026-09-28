// Command route-parity compares every public GET route of two deployments
// byte for byte and times them, for the master-data store cut-over
// (docs/postgres-master-data-store.md, rollout steps 4 and 6).
//
// It reads the routes from the Swagger spec, fills path parameters with ids
// it discovers from the reference deployment's list endpoints, requests each
// URL from both deployments once cold and then -warm more times, and reports
// every response that differs and the slowest routes. It exits 1 when any
// response differs or a candidate request takes -fail-after or longer.
//
//	go run ./cmd/route-parity -a http://localhost:18080 -b http://localhost:18081 -regions jp,en,tw,kr,cn
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const apiPrefix = "/api/v1"

// missingID is an id no region has, to compare not-found answers too.
const missingID = "999999999"

var missionFamilies = []string{"storyMissions", "characterMissionV2s", "normalMissions"}

type options struct {
	reference  string
	candidate  string
	regions    []string
	swagger    string
	skip       []string
	warmRounds int
	timeout    time.Duration
	failAfter  time.Duration
	maxDiffs   int
}

func main() {
	opts := options{}
	var regions, skip string
	flag.StringVar(&opts.reference, "a", "", "reference base URL, for example the Redis-backed deployment")
	flag.StringVar(&opts.candidate, "b", "", "candidate base URL, for example the PostgreSQL-backed deployment")
	flag.StringVar(&regions, "regions", "jp,en,tw,kr,cn", "comma-separated regions")
	flag.StringVar(&opts.swagger, "swagger", "internal/transport/http/swaggerdocs/swagger.json", "Swagger spec listing the routes")
	flag.StringVar(&skip, "skip", "/health,/build-info", "comma-separated route templates to leave out")
	flag.IntVar(&opts.warmRounds, "warm", 2, "warm requests per URL and deployment after the cold one")
	flag.DurationVar(&opts.timeout, "timeout", 30*time.Second, "per-request timeout")
	flag.DurationVar(&opts.failAfter, "fail-after", 3*time.Second, "fail when a candidate request takes this long")
	flag.IntVar(&opts.maxDiffs, "max-diffs", 20, "differing responses to print in full")
	flag.Parse()

	if opts.reference == "" || opts.candidate == "" {
		fmt.Fprintln(os.Stderr, "route-parity: -a and -b are required")
		flag.Usage()
		os.Exit(2)
	}
	opts.regions = splitList(regions)
	opts.skip = splitList(skip)

	report, err := run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "route-parity:", err)
		os.Exit(2)
	}
	report.print(os.Stdout, opts)
	if report.failed(opts.failAfter) {
		os.Exit(1)
	}
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// client requests one deployment.
type client struct {
	base string
	http *http.Client
}

type response struct {
	status   int
	body     []byte
	duration time.Duration
	err      error
}

func (c client) get(path string) response {
	started := time.Now()
	resp, err := c.http.Get(strings.TrimRight(c.base, "/") + apiPrefix + path)
	if err != nil {
		return response{err: err, duration: time.Since(started)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: body, duration: time.Since(started), err: err}
}

// result is one URL's comparison and timings.
type result struct {
	path                      string
	reference, candidate      response
	referenceWarm, candidWarm time.Duration
}

func (r result) equal() bool {
	return r.reference.err == nil && r.candidate.err == nil &&
		r.reference.status == r.candidate.status && bytes.Equal(r.reference.body, r.candidate.body)
}

func (r result) slowestCandidate() time.Duration {
	return max(r.candidate.duration, r.candidWarm)
}

type report struct {
	results []result
}

func run(opts options) (*report, error) {
	templates, err := routeTemplates(opts.swagger, opts.skip)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: opts.timeout}
	reference := client{base: opts.reference, http: httpClient}
	candidate := client{base: opts.candidate, http: httpClient}

	paths := expandRoutes(templates, opts.regions, newDiscoverer(reference))
	results := make([]result, 0, len(paths))
	for _, path := range paths {
		entry := result{path: path, reference: reference.get(path), candidate: candidate.get(path)}
		for range opts.warmRounds {
			entry.referenceWarm = max(entry.referenceWarm, reference.get(path).duration)
			entry.candidWarm = max(entry.candidWarm, candidate.get(path).duration)
		}
		results = append(results, entry)
	}
	return &report{results: results}, nil
}

// routeTemplates returns the public GET routes of the Swagger spec, sorted.
func routeTemplates(swaggerPath string, skip []string) ([]string, error) {
	body, err := os.ReadFile(swaggerPath)
	if err != nil {
		return nil, fmt.Errorf("read swagger: %w", err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &spec); err != nil {
		return nil, fmt.Errorf("parse swagger: %w", err)
	}
	skipped := make(map[string]bool, len(skip))
	for _, template := range skip {
		skipped[template] = true
	}
	templates := make([]string, 0, len(spec.Paths))
	for path, operations := range spec.Paths {
		if _, ok := operations["get"]; !ok || strings.HasPrefix(path, "/admin") || skipped[path] {
			continue
		}
		templates = append(templates, path)
	}
	sort.Strings(templates)
	return templates, nil
}

// discoverer finds path parameter values from the reference deployment.
type discoverer struct {
	client client
	cache  map[string][]string
}

func newDiscoverer(c client) *discoverer {
	return &discoverer{client: c, cache: make(map[string][]string)}
}

// values returns the first, middle and last values of field in the items of
// the list at path, or nil when the list cannot be read.
func (d *discoverer) values(path string, field string) []string {
	key := path + "#" + field
	if cached, ok := d.cache[key]; ok {
		return cached
	}
	resp := d.client.get(path)
	var found []string
	if resp.err == nil && resp.status == http.StatusOK {
		found = sample(fieldValues(resp.body, field))
	}
	d.cache[key] = found
	return found
}

// fieldValues returns field's values from a list response: the objects of
// its "items" array, or of the body itself when it is an array.
func fieldValues(body []byte, field string) []string {
	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	items := []map[string]any(nil)
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Items != nil {
		items = envelope.Items
	} else {
		_ = json.Unmarshal(body, &items)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item[field]; ok && value != nil {
			values = append(values, formatValue(value))
		}
	}
	return values
}

func formatValue(value any) string {
	if number, ok := value.(float64); ok {
		return fmt.Sprintf("%.0f", number)
	}
	return fmt.Sprint(value)
}

func sample(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	picked := []string{values[0], values[len(values)/2], values[len(values)-1]}
	unique := make([]string, 0, len(picked))
	for _, value := range picked {
		if !contains(unique, value) {
			unique = append(unique, value)
		}
	}
	return unique
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

// resource is the first segment of a route, which names its list endpoint.
func resource(template string) string {
	parts := strings.Split(strings.TrimPrefix(template, "/"), "/")
	return parts[0]
}

// expandRoutes fills every template's parameters and adds query variants of
// list endpoints. Ids come from the resource's list in the same region, plus
// an id no region has.
func expandRoutes(templates []string, regions []string, discover *discoverer) []string {
	var paths []string
	add := func(path string) {
		if !contains(paths, path) {
			paths = append(paths, path)
		}
	}

	for _, template := range templates {
		base := resource(template)
		switch {
		case strings.Contains(template, "/regions/"):
			// Availability across regions: ids of the first region's list.
			for _, region := range regions[:min(1, len(regions))] {
				ids := discover.values("/"+base+"/"+region+"/list?page_size=100", idField(base))
				for _, id := range append(ids, missingID) {
					add(fill(template, "", id, id))
				}
			}
		case !strings.Contains(template, "{region}"):
			add(template)
		default:
			for _, region := range regions {
				for _, path := range expandRegionRoute(template, base, region, discover) {
					add(path)
				}
			}
		}
	}
	return paths
}

// idSources names where a resource without a list endpoint of its own finds
// ids: a list path (with %s for the region) and the field holding them.
var idSources = map[string][2]string{
	"characterMissionV2ParameterGroups": {"/missions/%s/list?family=characterMissionV2s&page_size=100", "parameterGroupId"},
}

func expandRegionRoute(template, base, region string, discover *discoverer) []string {
	list := "/" + base + "/" + region + "/list?page_size=100"
	if source, ok := idSources[base]; ok {
		list = fmt.Sprintf(source[0], region)
	}
	switch {
	case strings.Contains(template, "{family}"):
		var paths []string
		for _, family := range missionFamilies {
			ids := discover.values("/missions/"+region+"/list?family="+family+"&page_size=100", "id")
			for _, id := range append(ids, missingID) {
				paths = append(paths, strings.NewReplacer("{region}", region, "{family}", family, "{id}", id).Replace(template))
			}
		}
		return paths
	case strings.Contains(template, "{unit}"):
		var paths []string
		for _, unit := range discover.values("/unitProfiles/"+region+"/list", "unit") {
			paths = append(paths, fill(template, region, "", unit))
		}
		return paths
	case strings.Contains(template, "{id}"):
		var paths []string
		for _, id := range append(discover.values(list, idField(base)), missingID) {
			paths = append(paths, fill(template, region, id, ""))
		}
		return paths
	case strings.HasSuffix(template, "/batch"):
		ids := discover.values(list, idField(base))
		return []string{fill(template, region, "", "") + "?ids=" + strings.Join(append(ids, missingID), ",")}
	case strings.HasSuffix(template, "/list"):
		path := fill(template, region, "", "")
		variants := []string{path, path + "?page=2&page_size=7", path + "?page_size=100&sort_order=desc"}
		if base == "missions" {
			variants = variants[:0]
			for _, family := range missionFamilies {
				variants = append(variants, path+"?family="+family, path+"?family="+family+"&page=2&page_size=7")
			}
		}
		return variants
	default:
		return []string{fill(template, region, "", "")}
	}
}

func idField(base string) string {
	if source, ok := idSources[base]; ok {
		return source[1]
	}
	if base == "unitProfiles" {
		return "unit"
	}
	return "id"
}

func fill(template, region, id, unit string) string {
	return strings.NewReplacer("{region}", region, "{id}", id, "{unit}", unit).Replace(template)
}

func (r *report) failed(failAfter time.Duration) bool {
	for _, entry := range r.results {
		if !entry.equal() || entry.slowestCandidate() >= failAfter {
			return true
		}
	}
	return false
}

func (r *report) print(out io.Writer, opts options) {
	statuses := map[int]int{}
	var differing []result
	for _, entry := range r.results {
		statuses[entry.reference.status]++
		if !entry.equal() {
			differing = append(differing, entry)
		}
	}
	fmt.Fprintf(out, "compared %d URLs: %d identical, %d different; reference statuses %v\n",
		len(r.results), len(r.results)-len(differing), len(differing), statuses)

	for index, entry := range differing {
		if index == opts.maxDiffs {
			fmt.Fprintf(out, "… %d more differences\n", len(differing)-index)
			break
		}
		fmt.Fprintf(out, "\nDIFF %s\n  a: %s\n  b: %s\n", entry.path, describe(entry.reference), describe(entry.candidate))
		if offset := firstDifference(entry.reference.body, entry.candidate.body); offset >= 0 {
			fmt.Fprintf(out, "  first difference at byte %d:\n  a: %s\n  b: %s\n", offset, excerpt(entry.reference.body, offset), excerpt(entry.candidate.body, offset))
		}
	}

	for _, side := range []struct {
		name string
		cold func(result) time.Duration
		warm func(result) time.Duration
	}{
		{"a", func(e result) time.Duration { return e.reference.duration }, func(e result) time.Duration { return e.referenceWarm }},
		{"b", func(e result) time.Duration { return e.candidate.duration }, func(e result) time.Duration { return e.candidWarm }},
	} {
		sorted := append([]result(nil), r.results...)
		sort.Slice(sorted, func(i, j int) bool { return side.cold(sorted[i]) > side.cold(sorted[j]) })
		over1s, over3s := 0, 0
		for _, entry := range sorted {
			slowest := max(side.cold(entry), side.warm(entry))
			if slowest >= time.Second {
				over1s++
			}
			if slowest >= 3*time.Second {
				over3s++
			}
		}
		fmt.Fprintf(out, "\n%s: %d URLs at or over 1s, %d at or over 3s; slowest cold (warm max):\n", side.name, over1s, over3s)
		for _, entry := range sorted[:min(10, len(sorted))] {
			fmt.Fprintf(out, "  %6d ms (%5d ms)  %s\n", side.cold(entry).Milliseconds(), side.warm(entry).Milliseconds(), entry.path)
		}
	}
}

func describe(resp response) string {
	if resp.err != nil {
		return "error: " + resp.err.Error()
	}
	return fmt.Sprintf("%d, %d bytes", resp.status, len(resp.body))
}

func firstDifference(left, right []byte) int {
	for index := range min(len(left), len(right)) {
		if left[index] != right[index] {
			return index
		}
	}
	if len(left) != len(right) {
		return min(len(left), len(right))
	}
	return -1
}

func excerpt(body []byte, offset int) string {
	start := max(0, offset-40)
	end := min(len(body), offset+80)
	return string(body[start:end])
}
