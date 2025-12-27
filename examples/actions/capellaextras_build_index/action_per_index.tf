locals {
  org_id = "aaaaaaaa-8f0c-22222-865e-bbbbbbbbbbbb"
  indexes = {
    idx1 = {
      org_id     = local.org_id
      index_keys = ["foo", "bar"]
    }
    idx2 = {
      org_id     = local.org_id
      index_keys = ["foo", "bar"]
    }
  }
}

resource "couchbase-capella_query_indexes" "index" {
  for_each        = local.indexes
  bucket_name     = couchbase-capella_bucket.new_free_tier_bucket.name
  cluster_id      = couchbase-capella_free_tier_cluster.new_free_tier_cluster.id
  organization_id = each.value.org_id
  project_id      = couchbase-capella_project.new_project.id
  index_name      = each.key
  index_keys      = each.value.index_keys
  with = {
    defer_build = true
  }
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.capellaextras_build_index.build_index[each.key]]
    }
  }
}


action "capellaextras_build_index" "build_index" {
  for_each = couchbase-capella_query_indexes.index
  config {
    bucket_name     = each.value.bucket_name
    cluster_id      = each.value.cluster_id
    organization_id = each.value.organization_id
    project_id      = each.value.project_id
    scope_name      = each.value.scope_name
    collection_name = each.value.collection_name
    index_names     = [each.key]
  }
}
