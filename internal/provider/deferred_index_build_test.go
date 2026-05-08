// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// mockIndexServer serves a minimal Capella index API for testing.
//
// Normal indexes: configured in indexStatuses; returned on every GET.
//
// Pending indexes: configured via setPending(name, status).  The first GET returns
// 404 (simulating the index being absent during Terraform's plan-phase Read).
// Every subsequent GET promotes the index to its configured status (simulating it
// being created by the Capella provider during apply, before this resource's Update
// runs).  This lets a single resource.TestStep verify the full single-apply scenario.
//
// On POST queryService/indexes the server increments buildCallCount and transitions
// every trigger-status index to "Building" so post-apply Reads return a non-trigger
// status and the empty-plan idempotency check passes.
type mockIndexServer struct {
	mu              sync.Mutex
	indexStatuses   map[string]string
	buildCallCount  int
	triggerStatuses map[string]bool
	// pendingIndexes: absent on first GET, then promoted to the stored status.
	pendingIndexes map[string]string
	getCallCounts  map[string]int
}

func newMockIndexServer(statuses map[string]string) (*httptest.Server, *mockIndexServer) {
	m := &mockIndexServer{
		indexStatuses:   statuses,
		triggerStatuses: map[string]bool{"Created": true},
		pendingIndexes:  make(map[string]string),
		getCallCounts:   make(map[string]int),
	}
	return httptest.NewServer(m), m
}

func (m *mockIndexServer) setStatus(indexName, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexStatuses[indexName] = status
}

// setPending marks indexName as absent on the first GET and present with the given
// status on all subsequent GETs.  The per-index GET counter is reset so that calling
// setPending in a PreConfig between test steps always produces a clean 404-then-promote
// sequence regardless of how many prior GETs occurred in earlier steps.
func (m *mockIndexServer) setPending(indexName, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingIndexes[indexName] = status
	m.getCallCounts[indexName] = 0
}

func (m *mockIndexServer) getBuildCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buildCallCount
}

func (m *mockIndexServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "indexBuildStatus"):
		parts := strings.Split(r.URL.Path, "/")
		indexName, _ := url.PathUnescape(parts[len(parts)-1])

		m.getCallCounts[indexName]++

		// Handle pending indexes: first GET → 404, subsequent GETs → promote to real status.
		if pendingStatus, isPending := m.pendingIndexes[indexName]; isPending {
			if m.getCallCounts[indexName] == 1 {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"code":    "not_found",
					"message": fmt.Sprintf("index %q not found", indexName),
				})
				return
			}
			m.indexStatuses[indexName] = pendingStatus
			delete(m.pendingIndexes, indexName)
		}

		status, ok := m.indexStatuses[indexName]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "not_found",
				"message": fmt.Sprintf("index %q not found", indexName),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})

	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/queryService/indexes"):
		m.buildCallCount++
		// Transition trigger-status indexes to "Building" so the post-apply Read
		// returns a non-trigger status and the empty-plan check passes.
		for idx, status := range m.indexStatuses {
			if m.triggerStatuses[status] {
				m.indexStatuses[idx] = "Building"
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// --- config helpers ---

func testDeferredIndexBuildProviderBlock(serverURL string) string {
	return fmt.Sprintf(`
provider "capellaextras" {
  host                 = %[1]q
  authentication_token = "test-token"
}
`, serverURL)
}

func testDeferredIndexBuildConfig(serverURL, orgID, projID, clusterID, bucket string, indexNames []string) string { //nolint:unparam
	quoted := make([]string, len(indexNames))
	for i, n := range indexNames {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return testDeferredIndexBuildProviderBlock(serverURL) + fmt.Sprintf(`
resource "capellaextras_deferred_index_build" "test" {
  organization_id = %[1]q
  project_id      = %[2]q
  cluster_id      = %[3]q
  bucket_name     = %[4]q
  index_names     = [%[5]s]
}
`, orgID, projID, clusterID, bucket, strings.Join(quoted, ", "))
}

func testDeferredIndexBuildConfigWithTriggers(serverURL, orgID, projID, clusterID, bucket string, indexNames, triggerStatuses []string) string {
	quotedIdx := make([]string, len(indexNames))
	for i, n := range indexNames {
		quotedIdx[i] = fmt.Sprintf("%q", n)
	}
	quotedTrig := make([]string, len(triggerStatuses))
	for i, s := range triggerStatuses {
		quotedTrig[i] = fmt.Sprintf("%q", s)
	}
	return testDeferredIndexBuildProviderBlock(serverURL) + fmt.Sprintf(`
resource "capellaextras_deferred_index_build" "test" {
  organization_id        = %[1]q
  project_id             = %[2]q
  cluster_id             = %[3]q
  bucket_name            = %[4]q
  index_names            = [%[5]s]
  build_trigger_statuses = [%[6]s]
}
`, orgID, projID, clusterID, bucket, strings.Join(quotedIdx, ", "), strings.Join(quotedTrig, ", "))
}

const (
	testOrgID     = "test-org-id"
	testProjID    = "test-proj-id"
	testClusterID = "test-cluster-id"
	testBucket    = "test-bucket"
)

// TestAccDeferredIndexBuildResource_allCreated verifies that when all indexes are in "Created"
// state, the resource triggers a build for all of them and records "Building" in state.
func TestAccDeferredIndexBuildResource_allCreated(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Created",
		"idx2": "Created",
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "bucket_name", testBucket),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_names.#", "2"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Building"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Building"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "id",
						fmt.Sprintf("%s/%s/%s/%s", testOrgID, testProjID, testClusterID, testBucket)),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "build_trigger_statuses.#", "1"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "build_trigger_statuses.0", "Created"),
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_alreadyBuilt verifies that when all indexes are already
// in "Online" state, no build is triggered and the state reflects the current statuses.
func TestAccDeferredIndexBuildResource_alreadyBuilt(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Online",
		"idx2": "Online",
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Online"),
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 0 {
		t.Errorf("expected 0 build API calls, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_partialBuild verifies that only indexes in a trigger
// status are built; already-built indexes are left untouched.
func TestAccDeferredIndexBuildResource_partialBuild(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Online",  // already built
		"idx2": "Created", // needs build
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Building"),
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_addIndex verifies that adding a new index to an existing
// resource triggers a build only for the new index.
func TestAccDeferredIndexBuildResource_addIndex(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Online",  // pre-existing built index
		"idx2": "Created", // new deferred index
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Step 1: manage only idx1, which is already built.
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_names.#", "1"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
				),
			},
			// Step 2: add idx2 which is in "Created" state; only idx2 should be built.
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_names.#", "2"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Building"),
				),
			},
		},
	})

	// Only one build call across both steps (step 1 had no trigger, step 2 built idx2).
	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_driftDetection verifies that when an index reverts to
// "Created" status outside Terraform (e.g. it was deleted and recreated as deferred),
// the resource detects the drift on the next plan and triggers a rebuild.
func TestAccDeferredIndexBuildResource_driftDetection(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Online",
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Step 1: idx1 is already Online, no build needed.
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1"},
				),
				Check: resource.TestCheckResourceAttr(
					"capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online",
				),
			},
			// Step 2: simulate drift — idx1 was externally deleted and recreated as deferred.
			// ModifyPlan should detect the "Created" status in state and force an Update.
			{
				PreConfig: func() {
					mock.setStatus("idx1", "Created")
				},
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1"},
				),
				Check: resource.TestCheckResourceAttr(
					"capellaextras_deferred_index_build.test", "index_statuses.idx1", "Building",
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_customTriggerStatuses verifies that build_trigger_statuses
// can be extended to include statuses beyond the default "Created".
func TestAccDeferredIndexBuildResource_customTriggerStatuses(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Error",
		"idx2": "Online",
	})
	// The mock only auto-transitions "Created" → "Building". For a custom status,
	// manually add it to triggerStatuses so the transition happens on build.
	mock.mu.Lock()
	mock.triggerStatuses["Error"] = true
	mock.mu.Unlock()

	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testDeferredIndexBuildConfigWithTriggers(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
					[]string{"Created", "Error"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "build_trigger_statuses.#", "2"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Building"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Online"),
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_missingIndexRecoveredInSameApply verifies the primary
// single-apply scenario: an index deleted outside Terraform (returns 404 during the
// plan-phase Read) must cause this resource to appear in the plan as needing an update,
// so that when the Capella provider recreates the index first (via the dependency chain),
// this resource's Update immediately triggers the deferred build — all in one apply.
//
// Step 1 establishes state with both indexes tracked as "Online".
// Step 2 simulates idx2 being deleted externally: setPending makes the first GET return 404
// (plan-phase Read), so ModifyPlan detects the missing entry and marks index_statuses unknown,
// forcing an Update. The second GET (apply-phase Update) promotes idx2 to "Created" and the
// build is triggered — all within the single step 2 apply.
func TestAccDeferredIndexBuildResource_missingIndexRecoveredInSameApply(t *testing.T) {
	mockSrv, mock := newMockIndexServer(map[string]string{
		"idx1": "Online",
		"idx2": "Online",
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Step 1: create the resource with both indexes already Online.
			// This establishes prior state so ModifyPlan runs in step 2.
			{
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Online"),
				),
			},
			// Step 2: idx2 is deleted externally between steps. setPending resets the GET
			// counter so the plan-phase Read sees 404 (omits idx2 from state), ModifyPlan
			// check 2 detects the gap and marks index_statuses unknown, and the apply-phase
			// Update sees idx2 as "Created" (second GET) and triggers the build.
			{
				PreConfig: func() {
					mock.setPending("idx2", "Created")
				},
				Config: testDeferredIndexBuildConfig(
					mockSrv.URL, testOrgID, testProjID, testClusterID, testBucket,
					[]string{"idx1", "idx2"},
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx2", "Building"),
				),
			},
		},
	})

	if got := mock.getBuildCallCount(); got != 1 {
		t.Errorf("expected 1 build API call, got %d", got)
	}
}

// TestAccDeferredIndexBuildResource_scopeAndCollection verifies that optional scope and
// collection attributes are forwarded correctly to the API.
func TestAccDeferredIndexBuildResource_scopeAndCollection(t *testing.T) {
	mockSrv, _ := newMockIndexServer(map[string]string{
		"idx1": "Online",
	})
	defer mockSrv.Close()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testDeferredIndexBuildProviderBlock(mockSrv.URL) + fmt.Sprintf(`
resource "capellaextras_deferred_index_build" "test" {
  organization_id = %[1]q
  project_id      = %[2]q
  cluster_id      = %[3]q
  bucket_name     = %[4]q
  scope_name      = "my-scope"
  collection_name = "my-collection"
  index_names     = ["idx1"]
}
`, testOrgID, testProjID, testClusterID, testBucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "scope_name", "my-scope"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "collection_name", "my-collection"),
					resource.TestCheckResourceAttr("capellaextras_deferred_index_build.test", "index_statuses.idx1", "Online"),
				),
			},
		},
	})
}
