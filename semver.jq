# semver.jq

# 1. Definitions
def semver_regex:
  # Regex handles Major.Minor.Patch-Pre+Build
  "^(?<major>\\d+)(?:\\.(?<minor>\\d+))?(?:\\.(?<patch>\\d+))?(?:-(?<pre>[^\\+]+))?(?:\\+.*)?$";

def parse_semver:
  capture(semver_regex)
  | .major |= tonumber
  | .minor |= (if . then tonumber else null end)
  | .patch |= (if . then tonumber else null end);

# 2. Main Logic
# Input is expected to be a JSON array: ["1.0.0", "1.0.1", ...]

# Clean the input: ensure we only process strings that look like semver
map(select(strings and test(semver_regex)))
|
if index($target) then
  # Priority 1: Exact string match
  $target
else
  # Priority 2: Fuzzy match
  ($target | parse_semver) as $t |

  # Convert list to objects for comparison
  map(
    . as $raw
    | ($raw | parse_semver)
    | .original = $raw
  )

  # Filter based on your logic
  | map(select(
      (.major == $t.major) and
      ($t.minor == null or .minor == $t.minor) and
      ($t.patch == null or .patch == $t.patch) and

      # Exclude suffixes (like -rc) unless exact match (handled above)
      (.pre == null)
  ))

  # Sort numerically (Major -> Minor -> Patch)
  | sort_by(.major, .minor, .patch)

  # Return the highest version found
  | last
  | .original // empty
end
