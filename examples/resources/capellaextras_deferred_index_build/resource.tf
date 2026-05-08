locals {
  org_id = "aaaaaaaa-8f0c-22222-865e-bbbbbbbbbbbb"
  indexes = {
    idx1 = { index_keys = ["foo", "bar"] }
    idx2 = { index_keys = ["baz"] }
    idx3 = { index_keys = ["qux", "quux"] }
  }
}

# Create all indexes with defer_build = true so the keyspace is only scanned once.
resource "couchbase-capella_query_indexes" "index" {
  for_each        = local.indexes
  organization_id = local.org_id
  project_id      = couchbase-capella_project.new_project.id
  cluster_id      = couchbase-capella_free_tier_cluster.new_free_tier_cluster.id
  bucket_name     = couchbase-capella_bucket.new_free_tier_bucket.name
  index_name      = each.key
  index_keys      = each.value.index_keys
  with = {
    defer_build = true
  }
}

# This resource tracks whether each index has been built and triggers a build for
# any index still in the "Created" state. Re-running terraform apply after an index
# is deleted and recreated (deferred) will automatically re-trigger the build.
resource "capellaextras_deferred_index_build" "indexes" {
  organization_id = local.org_id
  project_id      = couchbase-capella_project.new_project.id
  cluster_id      = couchbase-capella_free_tier_cluster.new_free_tier_cluster.id
  bucket_name     = couchbase-capella_bucket.new_free_tier_bucket.name

  index_names = [for idx in couchbase-capella_query_indexes.index : idx.index_name]

  depends_on = [couchbase-capella_query_indexes.index]
}
