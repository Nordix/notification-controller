#!/bin/bash
# git-csaf-vex-gen-ci.sh — generate CSAF VEX documents from fork git history
#
# Designed for Nordix forks where release tags follow the pattern:
#   v1.7.3-est-1, camel-3.14.10-est-1, kustomize/v1.24.0-est1
#
# The script auto-resolves the upstream base tag from the release tag,
# fetches it from the upstream repo, and uses it as the scan starting point.
#
# Only requires: git, jq, date (coreutils)
#
# Usage: ./git-csaf-vex-gen-ci.sh [repo-path] [product-purl] [OPTIONS]
#   repo-path:       path to the git repo (default: .)
#   product-purl:    PURL for the product (default: auto-detected as pkg:generic/<repo-name>@<latest-tag>)
#
# Options:
#   --release-tag TAG      the fork release tag (e.g. v1.7.3-est-1); auto-computes
#                          base tag by stripping -est[-N] suffix and fetches from upstream
#   --since-ref REF        tag/commit to scan from (overrides --release-tag auto-detection)
#   --output-dir DIR       where to write VEX files (default: vex-output)
#   --upstream-url URL     upstream repo URL for fetching base tags (auto-detected if omitted)
#   --per-cve              also emit individual per-CVE CSAF VEX files

set -euo pipefail

# ─── Defaults & constants ───────────────────────────────────────────────────

OUTPUT_DIR="vex-output"
SINCE_REF=""
RELEASE_TAG=""
UPSTREAM_URL=""
REPO_PATH=""
PRODUCT_PURL=""
PER_CVE=false

PUBLISHER_NAME="EST"
PUBLISHER_NS="https://github.com/Nordix"

# ─── Parse arguments ────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-dir)    OUTPUT_DIR="$2"; shift 2 ;;
    --since-ref)     SINCE_REF="$2"; shift 2 ;;
    --release-tag)   RELEASE_TAG="$2"; shift 2 ;;
    --upstream-url)  UPSTREAM_URL="$2"; shift 2 ;;
    --per-cve)       PER_CVE=true; shift ;;
    -*)              echo "Unknown option: $1" >&2; exit 1 ;;
    *)
      if [ -z "$REPO_PATH" ]; then
        REPO_PATH="$1"
      elif [ -z "$PRODUCT_PURL" ]; then
        PRODUCT_PURL="$1"
      else
        echo "Unexpected argument: $1" >&2; exit 1
      fi
      shift ;;
  esac
done

REPO_PATH="${REPO_PATH:-.}"

# ─── Validate ───────────────────────────────────────────────────────────────

command -v jq >/dev/null 2>&1 || { echo "Error: jq not found in PATH" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "Error: git not found in PATH" >&2; exit 1; }
command -v gh >/dev/null 2>&1 || { echo "Error: gh not found in PATH" >&2; exit 1; }

if ! git -C "$REPO_PATH" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Error: $REPO_PATH is not a git repository" >&2
  exit 1
fi

# Work inside the repo for the rest of the script
cd "$REPO_PATH"

# ─── Auto-detect release tag if not provided ────────────────────────────────

if [ -z "$RELEASE_TAG" ] && [ -z "$SINCE_REF" ]; then
  # Find the latest -est tag in the repo (exclude api/ prefixed Go module tags)
  RELEASE_TAG=$(git tag --sort=-creatordate | grep -v '^api/' | grep -E -- '-est(-.*)?$' | head -1 || true)
  if [ -n "$RELEASE_TAG" ]; then
    echo "Auto-detected release tag: $RELEASE_TAG"
  else
    # Last resort: scan commits by @est.tech authors
    first_est=$(git log --author="@est.tech" --format="%H" --reverse | head -1)
    if [ -n "$first_est" ]; then
      echo "No -est tag found, scanning from first @est.tech commit"
      SINCE_REF="$first_est"
    else
      echo "Error: No -est tag found, no @est.tech commits, and no --release-tag or --since-ref provided" >&2
      exit 1
    fi
  fi
fi

# ─── Upstream base tag resolution ───────────────────────────────────────────

if [ -n "$RELEASE_TAG" ] && [ -z "$SINCE_REF" ]; then
  # Strip -est suffix
  BASE_TAG=$(echo "$RELEASE_TAG" | sed -E 's/-est(-.*)?$//')
  echo "Release tag: $RELEASE_TAG"
  echo "Computed base tag: $BASE_TAG"

  # Fetch from upstream if not available locally
  if ! git rev-parse "refs/tags/${BASE_TAG}" >/dev/null 2>&1; then
    echo "Base tag $BASE_TAG not found locally, attempting to fetch from upstream..."

    # Auto-detect upstream URL if not provided
    if [ -z "$UPSTREAM_URL" ]; then

      REPO_SLUG=$(git remote get-url origin | sed 's|.*github\.com[:/]||; s|\.git$||')
      
      if command -v gh >/dev/null 2>&1; then
        UPSTREAM_URL=$(gh api "repos/${REPO_SLUG}" --jq '.parent.clone_url' 2>/dev/null || true)
      fi

      if [ -n "$UPSTREAM_URL" ]; then
        echo "Auto-detected upstream: $UPSTREAM_URL"
      else
        echo "Warning: Could not auto-detect upstream repo URL" >&2
        echo "  Use --upstream-url to specify it manually" >&2
      fi
    fi

    if [ -n "$UPSTREAM_URL" ]; then
      git remote get-url upstream >/dev/null 2>&1 || git remote add upstream "$UPSTREAM_URL"
      git fetch upstream "refs/tags/${BASE_TAG}:refs/tags/${BASE_TAG}" 2>/dev/null \
        && echo "Fetched tag $BASE_TAG from upstream" \
        || echo "Warning: Could not fetch tag $BASE_TAG from upstream" >&2
    fi
  fi

  if git rev-parse "refs/tags/${BASE_TAG}" >/dev/null 2>&1; then
    SINCE_REF="$BASE_TAG"
    echo "Using base tag as since-ref: $SINCE_REF"
  else
    echo "Error: Base tag $BASE_TAG not available locally or from upstream" >&2
    exit 1
  fi
fi

# ─── Auto-detect PRODUCT_PURL ──────────────────────────────────────────────

if [ -z "$PRODUCT_PURL" ]; then
  REPO_NAME=$(basename "$(git remote get-url origin 2>/dev/null | sed 's|\.git$||')")
  VERSION="${RELEASE_TAG:-$(git tag --sort=-creatordate | grep -m1 '^v' || echo "unknown")}"
  PRODUCT_PURL="pkg:generic/${REPO_NAME}@${VERSION}"
  echo "Auto-detected PRODUCT_PURL: $PRODUCT_PURL"
fi

PRODUCT_NAME=$(echo "$PRODUCT_PURL" | sed 's|^pkg:[^/]*/||; s|@.*||; s|?.*||')
PRODUCT_VERSION=$(echo "$PRODUCT_PURL" | sed 's|^[^@]*@||; s|?.*||')
VENDOR_NAME="$PUBLISHER_NAME"

# ─── Determine commit range and extract CVE-to-commit mapping ─

LOG_RANGE="${SINCE_REF}^..HEAD"

echo "Scanning commits since ${SINCE_REF:-first EST commit}"

# Single-pass: extract all CVE mentions with their commit hash and message
# Output: one JSON object per CVE (first commit wins)
declare -A CVE_MAP
CVE_ENTRIES_FILE=$(mktemp)
trap 'rm -f "$CVE_ENTRIES_FILE"' EXIT

while IFS= read -r line; do
  commit_hash="${line%% *}"
  rest="${line#* }"
  # Extract CVEs from this commit's subject+body
  cve_matches=$(echo "$rest" | grep -oE 'CVE-[0-9]{4}-[0-9]{4,}' || true)
  for cve in $cve_matches; do
    # Only record first occurrence (earliest commit due to --reverse)
    if [ -z "${CVE_MAP[$cve]:-}" ]; then
      CVE_MAP[$cve]="$commit_hash"
      commit_short="${commit_hash:0:12}"
      commit_msg=$(echo "$rest" | head -1)
      # Emit as newline-delimited JSON
      jq -n --compact-output \
        --arg cve "$cve" \
        --arg commit "$commit_short" \
        --arg note "Fixed via commit ${commit_short}: ${commit_msg}" \
        --arg remediation "Update to ${PRODUCT_NAME} ${PRODUCT_VERSION} or later (fork commit ${commit_short})" \
        '{
          cve: $cve,
          commit: $commit,
          notes: [{category: "description", text: $note}],
          product_status: {fixed: ["CSAFPID-0001"]},
          remediations: [{category: "vendor_fix", details: $remediation, product_ids: ["CSAFPID-0001"]}]
        }' >> "$CVE_ENTRIES_FILE"
      echo "   $cve (fixed in $commit_short)"
    fi
  done
done < <(git log $LOG_RANGE --reverse --format="%H %s %b")

cve_count="${#CVE_MAP[@]}"

if [ "$cve_count" -eq 0 ]; then
  echo "No CVE references found in git history"
  exit 0
fi

echo "Found $cve_count CVEs"

# ─── Timestamps & IDs ──────────────────────────────────────────────────────

NOW_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TODAY=$(date -u +"%Y-%m-%d")
TRACKING_ID="nordix-${PRODUCT_NAME}-vex-${TODAY}"

# ─── Helper: build CSAF VEX document wrapper ────────────────────────────────

build_csaf_doc() {
  local title="$1"
  local tracking_id="$2"
  local vulns_json="$3"

  jq -n \
    --arg title "$title" \
    --arg tracking_id "$tracking_id" \
    --arg now "$NOW_ISO" \
    --arg pub_name "$PUBLISHER_NAME" \
    --arg pub_ns "$PUBLISHER_NS" \
    --arg vendor "$VENDOR_NAME" \
    --arg prod_name "$PRODUCT_NAME" \
    --arg prod_version "$PRODUCT_VERSION" \
    --arg prod_fullname "${VENDOR_NAME} ${PRODUCT_NAME} ${PRODUCT_VERSION}" \
    --arg purl "$PRODUCT_PURL" \
    --argjson vulns "$vulns_json" \
    '{
      document: {
        category: "csaf_vex",
        csaf_version: "2.0",
        title: $title,
        publisher: {
          category: "vendor",
          name: $pub_name,
          namespace: $pub_ns
        },
        tracking: {
          id: $tracking_id,
          initial_release_date: $now,
          current_release_date: $now,
          status: "final",
          version: "1",
          revision_history: [{date: $now, number: "1", summary: "Initial VEX statement"}]
        },
        distribution: {tlp: {label: "WHITE"}}
      },
      product_tree: {
        branches: [{
          category: "vendor",
          name: $vendor,
          branches: [{
            category: "product_name",
            name: $prod_name,
            branches: [{
              category: "product_version",
              name: $prod_version,
              product: {
                name: $prod_fullname,
                product_id: "CSAFPID-0001",
                product_identification_helper: {purl: $purl}
              }
            }]
          }]
        }]
      },
      vulnerabilities: $vulns
    }'
}

# ─── Generate output ───────────────────────────────────────────────────────

mkdir -p "$OUTPUT_DIR"

# Build combined vulnerabilities array from newline-delimited JSON (single jq call)
VULNS_JSON=$(jq -s '[.[] | del(.commit)]' "$CVE_ENTRIES_FILE")

# Write combined document
COMBINED_FILE="$OUTPUT_DIR/combined.csaf.json"
build_csaf_doc \
  "VEX for ${PRODUCT_NAME} ${PRODUCT_VERSION} (Nordix fork)" \
  "$TRACKING_ID" \
  "$VULNS_JSON" > "$COMBINED_FILE"

# Write per-CVE documents
if [ "$PER_CVE" = true ]; then
  while IFS= read -r entry; do
    cve=$(echo "$entry" | jq -r '.cve')
    vuln_json=$(echo "$entry" | jq '[del(.commit)]')
    build_csaf_doc \
      "VEX: ${cve} fixed in ${PRODUCT_NAME} (Nordix fork)" \
      "nordix-${PRODUCT_NAME}-${cve}-${TODAY}" \
      "$vuln_json" > "$OUTPUT_DIR/${cve}.csaf.json"
  done < "$CVE_ENTRIES_FILE"
fi

# ─── Summary ───────────────────────────────────────────────────────────────

echo ""
echo "Combined CSAF VEX: $COMBINED_FILE"
echo "  Vulnerabilities: $cve_count"
echo "  Product: $PRODUCT_PURL"
echo "  Tracking ID: $TRACKING_ID"

if [ "$PER_CVE" = true ]; then
  per_cve_count=$(find "$OUTPUT_DIR" -name "CVE-*.csaf.json" | wc -l)
  echo "  Per-CVE files: $per_cve_count"
fi
