# semver.jq - Semantic versioning utilities for jq

# Regex pattern for semantic versioning (supports partial versions)
def semver_regex:
  "^(?<major>0|[1-9][0-9]*)(?:\\.(?<minor>0|[1-9][0-9]*)(?:\\.(?<patch>0|[1-9][0-9]*)(?:-(?<prerelease>[^\\+]+))?(?:\\+(?<build>.*))?)?)?$";

# Parse a semver string into a structured object
# Input: string like "1.2.3" or "1.2.3-beta+build"
# Output: { major, minor, patch, pre, build } with numbers where applicable
def parse_semver:
  capture(semver_regex)
  | {
      major: (.major | tonumber),
      minor: (if .minor == null then null else (.minor | tonumber) end),
      patch: (if .patch == null then null else (.patch | tonumber) end),
      pre: .prerelease,
      build: .build
    };

# Sort an array of semver strings in ascending order
# Input: array of semver strings
# Output: sorted array of semver strings
def sort_semver:
  map(select(strings and test(semver_regex)))
  | map(. as $raw | ($raw | parse_semver) | .original = $raw)
  | sort_by(.major, .minor // 0, .patch // 0, (if .pre then 0 else 1 end), .pre // "")
  | map(.original);

# Sort an array of semver strings in descending order
# Input: array of semver strings
# Output: sorted array of semver strings (highest first)
def sort_semver_desc:
  sort_semver | reverse;

# Check if a string is a valid semver
# Input: string
# Output: boolean
def is_valid_semver:
  test(semver_regex);

# Compare two semver strings
# Returns: -1 if a < b, 0 if a == b, 1 if a > b
def compare_semver($a; $b):
  ($a | parse_semver) as $pa |
  ($b | parse_semver) as $pb |
  if $pa.major < $pb.major then -1
  elif $pa.major > $pb.major then 1
  elif ($pa.minor // 0) < ($pb.minor // 0) then -1
  elif ($pa.minor // 0) > ($pb.minor // 0) then 1
  elif ($pa.patch // 0) < ($pb.patch // 0) then -1
  elif ($pa.patch // 0) > ($pb.patch // 0) then 1
  else 0
  end;

# Match a target version against an array of versions
# Returns the best matching version (highest that matches the target pattern)
# Input: array of semver strings
# $target: version pattern to match (e.g., "2" matches "2.x.x", "2.1" matches "2.1.x")
def match_semver($target):
  map(select(strings and test(semver_regex)))
  |
  if index($target) then
    # Priority 1: Exact string match
    $target
  else
    # Priority 2: Fuzzy match based on target specificity
    ($target | parse_semver) as $t |

    # Convert list to objects for comparison
    map(
      . as $raw
      | ($raw | parse_semver)
      | .original = $raw
    )

    # Filter based on target pattern
    | map(select(
        (.major == $t.major) and
        ($t.minor == null or .minor == $t.minor) and
        ($t.patch == null or .patch == $t.patch) and
        # Exclude prereleases unless exact match (handled above)
        (.pre == null)
    ))

    # Sort numerically (Major -> Minor -> Patch)
    | sort_by(.major, .minor // 0, .patch // 0)

    # Return the highest version found
    | last
    | .original // empty
  end;
